package session

import (
	"sync"
	"testing"
	"time"
)

func fixedClock() func() time.Time {
	t := time.Unix(1_700_000_000, 0).UTC()
	return func() time.Time { return t }
}

// collect reads until the bus is closed and its channel drains.
func collect(b *bus) []Event {
	var got []Event
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range b.events() {
			got = append(got, ev)
		}
	}()
	b.close()
	<-done
	return got
}

func TestBusAssignsMonotonicSequenceAndTimestamp(t *testing.T) {
	b := newBus(16, fixedClock())
	for i := 0; i < 3; i++ {
		b.publish(Event{Kind: EvStoryStarted})
	}

	got := collect(b)
	if len(got) != 3 {
		t.Fatalf("want 3 events, got %d", len(got))
	}
	for i, ev := range got {
		if want := uint64(i + 1); ev.Seq != want {
			t.Errorf("event %d: seq = %d, want %d", i, ev.Seq, want)
		}
		if ev.At != time.Unix(1_700_000_000, 0).UnixMilli() {
			t.Errorf("event %d: At not stamped, got %d", i, ev.At)
		}
	}
}

// The whole reason the bus exists: a consumer that never reads must not be able
// to block the agent's output goroutine.
func TestBusNeverBlocksWhenConsumerIsAbsent(t *testing.T) {
	b := newBus(64, fixedClock())
	defer b.stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < softCap*4; i++ {
			b.publish(Event{Kind: EvAgentText, Text: "chatter"})
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("publish blocked with no consumer reading")
	}

	if _, dropped := b.stats(); dropped == 0 {
		t.Error("expected chatter to be trimmed when the consumer never reads")
	}
}

// Agent chatter is expendable; run and story lifecycle events are not. Under
// sustained pressure the guaranteed kinds must still be delivered, in order.
func TestBusTrimsChatterButKeepsGuaranteedKinds(t *testing.T) {
	b := newBus(1<<16, fixedClock())

	const guaranteed = 20
	for i := 0; i < guaranteed; i++ {
		for j := 0; j < hardCap; j++ {
			b.publish(Event{Kind: EvAgentText, Text: "chatter"})
		}
		b.publish(Event{Kind: EvStoryDone, StoryID: "US-001", Attempt: i})
	}

	var seen []int
	for _, ev := range collect(b) {
		if ev.Kind == EvStoryDone {
			seen = append(seen, ev.Attempt)
		}
	}

	if len(seen) != guaranteed {
		t.Fatalf("delivered %d story.done events, want %d — guaranteed kinds must survive backpressure", len(seen), guaranteed)
	}
	for i, got := range seen {
		if got != i {
			t.Fatalf("story.done out of order at %d: got attempt %d", i, got)
		}
	}
}

// A silent gap is worse than a loud one: the consumer would believe it had the
// complete stream. The count must ride on the next delivered event.
func TestDropCountIsReportedOnTheFollowingEvent(t *testing.T) {
	b := newBus(1<<16, fixedClock())

	for i := 0; i < softCap*3; i++ {
		b.publish(Event{Kind: EvAgentText})
	}
	b.publish(Event{Kind: EvStoryDone, StoryID: "marker"})

	var total uint64
	for _, ev := range collect(b) {
		total += ev.Dropped
	}
	if total == 0 {
		t.Error("chatter was trimmed but no event reported a Dropped count")
	}

	if _, lifetime := b.stats(); lifetime != total {
		t.Errorf("reported drops = %d, lifetime stat = %d; every drop must be accounted for", total, lifetime)
	}
}

// Regression: an earlier design emitted a dedicated drop-notice event per
// publish. Notices are undroppable, so under backpressure the queue filled with
// them and real events could no longer get through.
func TestDropReportingCannotStarveRealEvents(t *testing.T) {
	b := newBus(1<<16, fixedClock())

	for i := 0; i < softCap*2; i++ {
		b.publish(Event{Kind: EvAgentText})
	}
	b.publish(Event{Kind: EvRunComplete, PRD: "checkout"})

	for _, ev := range collect(b) {
		if ev.Kind == EvRunComplete && ev.PRD == "checkout" {
			return
		}
	}
	t.Fatal("run.complete never delivered; drop reporting starved the real event")
}

func TestReplayReturnsEventsAfterSeq(t *testing.T) {
	b := newBus(16, fixedClock())
	defer b.stop()

	for i := 0; i < 5; i++ {
		b.publish(Event{Kind: EvStoryStarted})
	}

	evs, complete := b.replay(2)
	if !complete {
		t.Error("ring holds everything; replay should be complete")
	}
	if len(evs) != 3 {
		t.Fatalf("want 3 events after seq 2, got %d", len(evs))
	}
	if evs[0].Seq != 3 {
		t.Errorf("first replayed seq = %d, want 3", evs[0].Seq)
	}
}

func TestReplayReportsIncompleteOnceRingHasRolled(t *testing.T) {
	b := newBus(4, fixedClock())
	defer b.stop()

	for i := 0; i < 10; i++ {
		b.publish(Event{Kind: EvStoryStarted})
	}

	if _, complete := b.replay(0); complete {
		t.Error("replay from 0 should report incomplete after the ring rolled")
	}
	if _, complete := b.replay(9); !complete {
		t.Error("replay from 9 should be complete; seq 10 is still retained")
	}
}

func TestRingIsBounded(t *testing.T) {
	const size = 8
	b := newBus(size, fixedClock())
	defer b.stop()

	for i := 0; i < size*20; i++ {
		b.publish(Event{Kind: EvAgentText})
	}

	b.mu.Lock()
	n, c := b.ringLen, cap(b.ring)
	retained := b.retainedLocked()
	b.mu.Unlock()

	if n != size {
		t.Errorf("ring length = %d, want %d", n, size)
	}
	// The circular buffer is allocated once and never reallocated.
	if c != size {
		t.Errorf("ring backing array = %d, want exactly %d", c, size)
	}
	// Oldest-first ordering must survive wraparound, or Replay returns garbage.
	if len(retained) != size {
		t.Fatalf("retained %d events, want %d", len(retained), size)
	}
	for i := 1; i < len(retained); i++ {
		if retained[i].Seq <= retained[i-1].Seq {
			t.Fatalf("ring not ordered oldest-first across wraparound: seq %d follows %d",
				retained[i].Seq, retained[i-1].Seq)
		}
	}
	if want := uint64(size*20 - size + 1); retained[0].Seq != want {
		t.Errorf("oldest retained seq = %d, want %d", retained[0].Seq, want)
	}
}

func TestConcurrentPublishersAreSerialised(t *testing.T) {
	b := newBus(1<<16, fixedClock())

	const writers, each = 8, 200
	var wg sync.WaitGroup
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				b.publish(Event{Kind: EvStoryStarted})
			}
		}()
	}
	wg.Wait()

	got := collect(b)
	if len(got) != writers*each {
		t.Fatalf("delivered %d events, want %d", len(got), writers*each)
	}
	for i, ev := range got {
		if want := uint64(i + 1); ev.Seq != want {
			t.Fatalf("event %d: seq = %d, want %d — sequence must be gapless and ordered", i, ev.Seq, want)
		}
	}
}

func TestCloseIsIdempotentAndDrains(t *testing.T) {
	b := newBus(16, fixedClock())
	b.publish(Event{Kind: EvRunStopped})

	got := collect(b)
	if len(got) != 1 {
		t.Errorf("close should drain queued events, got %d", len(got))
	}

	b.close() // must not panic or deadlock on a second call
	b.stop()  // escalating after a graceful close must also be safe

	// Publishing after shutdown must not panic — shutdown races are normal.
	b.publish(Event{Kind: EvRunStopped})
}
