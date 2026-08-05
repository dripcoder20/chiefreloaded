import type { LoopEvent } from "../platform";

/**
 * The log buffer. This is the one place in the app where performance is a
 * design constraint rather than an afterthought.
 *
 * A busy agent produces hundreds of events a second. Three rules keep that from
 * melting the UI:
 *
 *  1. The buffer is a plain array in a closure, never `$state`. Wrapping twenty
 *     thousand objects in a deep reactive proxy is the single fastest way to
 *     kill this app. A lone `version` counter drives every derived read instead.
 *  2. Incoming batches are queued and flushed once per animation frame. That
 *     caps UI work at 60 updates a second no matter the event rate — and when
 *     the window is occluded the browser stops calling rAF entirely, which is
 *     exactly the backpressure we want, for free.
 *  3. Buffers are per-PRD and survive switching between them. chief clears its
 *     log on switch, which with parallel runs is simply data loss.
 *
 * Reads are scoped to a story. A PRD's log is the concatenation of every story
 * it has run plus the run-level chatter between them, which for a long PRD is
 * tens of thousands of lines with no way to answer "what did US-004 actually
 * do". Filtering happens on read and is memoised per frame, so the buffer stays
 * one flat append-only array — the shape the ring depends on.
 */

/** How many events to keep per PRD before trimming the oldest. */
const CAP = 20_000;

/** Events are retained per PRD so switching away does not lose the log. */
const buffers = new Map<string, LoopEvent[]>();
/** Highest sequence seen per PRD, for gap detection. */
const lastSeq = new Map<string, number>();
/** Events dropped by the Go side, per PRD. Surfaced so a gap is never silent. */
const dropped = new Map<string, number>();

let pending: LoopEvent[] = [];
let frame: number | null = null;

/** The single signal every derived read depends on. */
let version = $state(0);

/**
 * How many events may wait for a flush.
 *
 * Needed because rAF does not fire while the window is hidden — which is the
 * backpressure this design wants, but it means a minimised window during a long
 * run would otherwise grow this queue for as long as the run lasts. Anything
 * beyond the cap is older than the ring would keep anyway.
 */
const PENDING_CAP = CAP;

/** Queue a batch. Cheap: no reactivity is touched here. */
export function ingest(events: LoopEvent[]): void {
  if (events.length === 0) return;
  pending.push(...events);

  if (pending.length > PENDING_CAP) {
    const over = pending.length - PENDING_CAP;
    // Count the discards so the log still admits it is incomplete rather than
    // presenting a gap as continuous output.
    for (const ev of pending.slice(0, over)) {
      const key = ev.prd || "";
      dropped.set(key, (dropped.get(key) ?? 0) + 1);
    }
    pending.splice(0, over);
  }

  scheduleFlush();
}

function scheduleFlush(): void {
  if (frame !== null) return;
  frame = requestAnimationFrame(() => {
    frame = null;
    flush();
  });
}

function flush(): void {
  if (pending.length === 0) return;

  const batch = pending;
  pending = [];

  for (const ev of batch) {
    const key = ev.prd || "";
    let buf = buffers.get(key);
    if (!buf) {
      buf = [];
      buffers.set(key, buf);
    }

    // A gap means the Go side trimmed chatter while we were behind. Record it
    // so the UI can say so rather than quietly presenting a partial log as
    // complete.
    if (ev.dropped) {
      dropped.set(key, (dropped.get(key) ?? 0) + ev.dropped);
    }
    lastSeq.set(key, Math.max(lastSeq.get(key) ?? 0, ev.seq));

    buf.push(ev);
    if (buf.length > CAP) {
      buf.splice(0, buf.length - CAP);
    }
  }

  version++;
}

/**
 * Story IDs seen for a PRD, in the order they first appeared.
 *
 * Derived from the buffer rather than the PRD document so it reflects what
 * actually ran: a story with no output never appears, and a story whose events
 * have aged out of the ring stops being offered.
 */
export function storiesFor(prd: string): string[] {
  version;
  const seen: string[] = [];
  const known = new Set<string>();
  for (const ev of buffers.get(prd) ?? []) {
    const id = ev.storyId;
    if (!id || known.has(id)) continue;
    known.add(id);
    seen.push(id);
  }
  return seen;
}

/** The last filtered view, so repeated reads within one frame cost nothing. */
let viewKey = "";
let viewVersion = -1;
let viewValue: LoopEvent[] = [];

/**
 * Events for a PRD, oldest first, optionally narrowed to one story.
 *
 * A fresh array is returned whenever the buffer has changed. That is load
 * bearing: `$derived` compares by reference, so handing back the same
 * mutated-in-place array would leave every reader frozen at whatever it first
 * rendered. The memo keeps that from costing a filter pass per read.
 *
 * Run-level events carry no story, so they appear only in the unfiltered view.
 */
export function logFor(prd: string, storyId?: string): LoopEvent[] {
  version; // establish the dependency
  const key = `${prd}\u0000${storyId ?? ""}`;
  if (viewKey === key && viewVersion === version) return viewValue;

  const all = buffers.get(prd) ?? [];
  viewKey = key;
  viewVersion = version;
  viewValue = storyId ? all.filter((ev) => ev.storyId === storyId) : all.slice();
  return viewValue;
}

/** How many events the backend discarded for this PRD. */
export function droppedFor(prd: string): number {
  version;
  return dropped.get(prd) ?? 0;
}

/** Highest sequence seen for a PRD; the resume point for Replay. */
export function seqFor(prd: string): number {
  return lastSeq.get(prd) ?? 0;
}

/** Highest sequence seen across every PRD. */
export function highestSeq(): number {
  let max = 0;
  for (const v of lastSeq.values()) max = Math.max(max, v);
  return max;
}

export function clear(prd?: string): void {
  // Drop the memo too, or a cleared PRD keeps serving its old view.
  viewKey = "";
  viewVersion = -1;
  viewValue = [];
  if (prd === undefined) {
    buffers.clear();
    lastSeq.clear();
    dropped.clear();
  } else {
    buffers.delete(prd);
    lastSeq.delete(prd);
    dropped.delete(prd);
  }
  version++;
}

/** Test seam: flush synchronously instead of waiting for a frame. */
export function flushNow(): void {
  if (frame !== null) {
    cancelAnimationFrame(frame);
    frame = null;
  }
  flush();
}
