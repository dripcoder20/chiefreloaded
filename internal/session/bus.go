package session

import (
	"sync"
	"time"
)

// bus is the session's event fan-out.
//
// The problem it solves: chief's loop.Loop.Events() is a buffered channel of 100
// whose producer *blocks* when full. That producer is the goroutine scanning the
// agent's stdout. If a consumer stalls — an occluded webview, a paused debugger,
// a slow test — backpressure propagates all the way into the agent subprocess and
// the build stops. Nothing downstream of the agent may ever block.
//
// Structure: publish appends to an unbounded internal queue and returns. A pump
// goroutine drains that queue into the subscriber channel with ordinary blocking
// sends. Backpressure therefore shows up as queue growth rather than as a stalled
// producer, and growth is bounded by trimming:
//
//   - coalescible events (agent chatter) are discarded oldest-first once the
//     queue passes softCap;
//   - every other kind is retained, because losing a story.done would corrupt the
//     caller's model of the world.
//
// Discards are never announced with their own event. An earlier version did that
// and it was actively harmful: one notice per publish while backpressured meant
// the queue filled with notices, which are themselves undroppable, and the real
// events could no longer get through. A signal that amplifies under exactly the
// condition it reports is the wrong shape. Instead the count rides along on the
// next event actually delivered, as Event.Dropped — the gap is announced at the
// point of discontinuity and cannot feed back on itself.
type bus struct {
	mu  sync.Mutex
	seq uint64

	// Retained history for Replay, as a true circular buffer. An append-and-trim
	// slice was the obvious alternative but costs an O(ringCap) copy on every
	// publish once full — at agent event rates that is thousands of element
	// copies per second for nothing — and lets the backing array grow past cap.
	ring     []Event
	ringHead int // index of the oldest retained event
	ringLen  int
	ringCap  int

	pending  []Event // queue awaiting delivery to the subscriber
	dropped  uint64  // discarded since the last successful delivery
	total    uint64  // discarded over the session's lifetime
	closed   bool
	signal   chan struct{} // buffered(1); wakes the pump
	quit     chan struct{} // closed by stop; lets the pump abandon a blocked send
	quitOnce sync.Once
	out      chan Event
	pumpDone chan struct{}

	now func() time.Time
}

const (
	defaultRingSize = 4096

	// Queue depth the trimmer aims for. Generous enough that an ordinary UI
	// hiccup costs nothing, small enough that a wedged consumer cannot grow the
	// queue without bound.
	softCap = 2048

	// Trimming only starts here, and then goes all the way back down to softCap.
	//
	// The hysteresis is load-bearing, not tuning. Trimming at softCap on every
	// publish means a full O(queue) scan per event for as long as the consumer is
	// behind — quadratic work precisely when the system is already struggling.
	// Scanning once per softCap events instead amortises it to O(1) per event.
	hardCap = softCap * 2

	outBuffer = 256
)

func newBus(ringSize int, now func() time.Time) *bus {
	if ringSize <= 0 {
		ringSize = defaultRingSize
	}
	if now == nil {
		now = time.Now
	}
	b := &bus{
		ring:     make([]Event, ringSize),
		ringCap:  ringSize,
		signal:   make(chan struct{}, 1),
		quit:     make(chan struct{}),
		out:      make(chan Event, outBuffer),
		pumpDone: make(chan struct{}),
		now:      now,
	}
	go b.pump()
	return b
}

// publish stamps ev with the next sequence number and timestamp, retains it for
// replay, and queues it for delivery. It never blocks.
func (b *bus) publish(ev Event) Event {
	b.mu.Lock()

	b.seq++
	ev.Seq = b.seq
	if ev.At == 0 {
		ev.At = b.now().UnixMilli()
	}

	b.retainLocked(ev)

	if !b.closed {
		b.pending = append(b.pending, ev)
		b.trimLocked()
	}
	b.mu.Unlock()

	b.wake()
	return ev
}

// retainLocked stores ev in the replay ring, evicting the oldest entry once
// full. O(1). Callers hold b.mu.
func (b *bus) retainLocked(ev Event) {
	if b.ringLen < b.ringCap {
		b.ring[(b.ringHead+b.ringLen)%b.ringCap] = ev
		b.ringLen++
		return
	}
	b.ring[b.ringHead] = ev
	b.ringHead = (b.ringHead + 1) % b.ringCap
}

// retainedLocked returns the ring contents oldest-first. Callers hold b.mu.
func (b *bus) retainedLocked() []Event {
	out := make([]Event, 0, b.ringLen)
	for i := 0; i < b.ringLen; i++ {
		out = append(out, b.ring[(b.ringHead+i)%b.ringCap])
	}
	return out
}

// trimLocked discards chatter, oldest first, once the queue passes hardCap,
// taking it back down to softCap. Guaranteed kinds are never discarded, so a
// queue made entirely of them will exceed softCap — that is intended: dropping a
// story.done to save memory would corrupt the caller's model of the world.
// Callers hold b.mu.
func (b *bus) trimLocked() {
	if len(b.pending) <= hardCap {
		return
	}

	need := len(b.pending) - softCap
	kept := b.pending[:0]
	for _, ev := range b.pending {
		if need > 0 && ev.Kind.coalescible() {
			need--
			b.dropped++
			b.total++
			continue
		}
		kept = append(kept, ev)
	}
	b.pending = kept
}

func (b *bus) wake() {
	select {
	case b.signal <- struct{}{}:
	default: // already signalled; the pump will see the new work
	}
}

// pump moves queued events to the subscriber. It is the only goroutine that
// writes to b.out, so it is also the only place a blocking send is allowed.
func (b *bus) pump() {
	defer close(b.pumpDone)

	for {
		b.mu.Lock()
		for len(b.pending) == 0 {
			if b.closed {
				b.mu.Unlock()
				close(b.out)
				return
			}
			b.mu.Unlock()
			select {
			case <-b.signal:
			case <-b.quit:
				close(b.out)
				return
			}
			b.mu.Lock()
		}

		ev := b.pending[0]
		b.pending = b.pending[1:]
		if b.dropped > 0 {
			// Report the gap on the first event to follow it.
			ev.Dropped = b.dropped
			b.dropped = 0
		}
		b.mu.Unlock()

		select {
		case b.out <- ev:
		case <-b.quit:
			// Hard shutdown: abandon whatever is still queued rather than block
			// forever on a subscriber that has stopped reading.
			close(b.out)
			return
		}
	}
}

// events returns the subscriber channel, closed once close is called and the
// queue has drained.
func (b *bus) events() <-chan Event { return b.out }

// replay returns every retained event after sinceSeq, in order. complete is false
// when the ring has rolled past sinceSeq, meaning the caller has an unrecoverable
// gap and should take a Snapshot instead.
func (b *bus) replay(sinceSeq uint64) (evs []Event, complete bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.ringLen == 0 {
		return nil, true
	}

	retained := b.retainedLocked()
	complete = sinceSeq+1 >= retained[0].Seq
	for _, ev := range retained {
		if ev.Seq > sinceSeq {
			evs = append(evs, ev)
		}
	}
	return evs, complete
}

// stats reports the current sequence and lifetime dropped count.
func (b *bus) stats() (seq, dropped uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq, b.total
}

// close stops accepting new events and lets the pump deliver what is already
// queued before closing the subscriber channel.
//
// It does not wait for that to finish. Waiting would require a subscriber to be
// reading, and making shutdown depend on the consumer is exactly the coupling
// this type exists to avoid — a caller that has already walked away would hang
// here forever. Callers that need to know the stream ended should read until the
// channel closes; callers that have stopped reading should use stop.
//
// Safe to call more than once, and safe to publish after.
func (b *bus) close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.mu.Unlock()

	b.wake()
}

// stop shuts down immediately, discarding anything still queued, and waits for
// the pump to exit. Use it when no one is reading the channel; use close when a
// subscriber is still draining. Calling stop after close is fine and is how a
// graceful shutdown escalates when the consumer never came back.
func (b *bus) stop() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()

	// Tracked separately from `closed`: close() sets that flag without closing
	// quit, so keying the close on it would let stop-after-close skip the wakeup
	// and then block on pumpDone forever.
	b.quitOnce.Do(func() { close(b.quit) })
	<-b.pumpDone
}
