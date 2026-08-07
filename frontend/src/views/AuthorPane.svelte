<script lang="ts">
  import { onDestroy, onMount, tick } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import "@xterm/xterm/css/xterm.css";
  import { api, onAuthorData, onAuthorExit, type AuthorEvent, type AuthorExit } from "../platform";
  import { app, publishIssues, refresh, selectPrd } from "../stores/app.svelte";
  import { errorMessage } from "../stores/errors";
  import { resolveTerminalKey } from "../lib/terminalInput";
  import { external } from "../lib/externalLink";

  /**
   * The interactive agent session that writes a PRD.
   *
   * chief quits its TUI, hands the terminal to the agent and relaunches itself
   * afterwards — impossible in a window. This is a real terminal, themed to the
   * app's own tokens so it reads as part of the interface rather than a black
   * box pasted into it. Being a real terminal is the point: slash commands,
   * lettered clarifying questions, permission prompts and Ctrl-C all work
   * because nothing is being reinterpreted.
   */

  /**
   * `active` is true only while the New PRD / Edit PRD tab is the visible one.
   *
   * The component stays mounted for the app's whole lifetime so that switching
   * tabs never tears down the session, its terminal, or this component's state.
   * When the tab is hidden the pane is `display:none`; going back to visible is
   * what drives the refit below.
   */
  let { active = true }: { active?: boolean } = $props();

  let host = $state<HTMLDivElement | null>(null);
  let term: Terminal | null = null;
  let fit: FitAddon | null = null;
  let observer: ResizeObserver | null = null;
  let unsubscribe: Array<() => void> = [];

  /**
   * The session id stands in as PENDING between asking for a session and being
   * told its id. Everything that needs a real id — writing keystrokes, pushing
   * the terminal size — has to wait for one, and what arrives in the meantime is
   * held rather than dropped. See adoptSession.
   */
  const PENDING = "pending";

  let sessionId = $state<string | null>(null);
  let starting = $state(false);
  let result = $state<AuthorExit | null>(null);
  let error = $state<string | null>(null);

  /**
   * Output that arrived before the session id did.
   *
   * Go starts pumping the PTY before Start() returns, so the agent's first
   * chunks can cross the bridge while this pane still holds PENDING. Those
   * chunks are the terminal setup — alternate screen, scroll region, clear —
   * and discarding them left the emulator in a state the agent never asked for,
   * which showed up as text drawn over text.
   */
  let earlyOutput: AuthorEvent[] = [];

  /**
   * Where this PRD publishes its stories, read from its own workflow settings.
   * The pane no longer chooses it — that moved to the New PRD dialog and to PRD
   * Settings — but it still decides whether publishing is offered once the
   * document has been written.
   */
  let issueDestination = $state("");

  const running = $derived(sessionId !== null && sessionId !== PENDING && result === null);

  /**
   * The PRD this pane is editing, or null when it is creating one.
   *
   * Read from the store's authorTarget rather than selectedPrd: selecting a
   * different PRD in the rail must not silently re-point a live editing session
   * at another document.
   */
  const target = $derived(app.authorTarget);
  const editing = $derived(target?.kind === "edit" ? target.prd : null);
  /** The PRD this conversation belongs to, whether it is new or being revised. */
  const conversationPrd = $derived(target?.prd ?? null);

  /**
   * Choosing Edit PRD in the rail is the decision — the pane does not ask again.
   * The guard is on the target, so returning to the tab or re-selecting the same
   * PRD cannot start a second session for it.
   *
   * start() owns this rather than the effect, so that clearing it is enough to
   * make the pane willing to open a second conversation for the same PRD.
   */
  let startedFor = $state<string | null>(null);
  $effect(() => {
    const prd = conversationPrd;
    if (!prd || startedFor === prd || sessionId) return;
    void start(target?.kind ?? "new", prd);
  });

  // Follow the selection, so publishing is offered against this PRD's own
  // settings rather than those of the one looked at before it.
  $effect(() => {
    const prd = conversationPrd;
    issueDestination = "";
    if (prd) void loadDestination(prd);
  });

  async function loadDestination(prd: string): Promise<void> {
    try {
      const workflow = await api.prd.workflow(prd);
      if (conversationPrd === prd) issueDestination = workflow?.issueDestination ?? "";
    } catch {
      // Publishing is an extra; not knowing where to publish must not disturb
      // the conversation itself.
    }
  }

  function makeTerminal(): Terminal {
    const css = getComputedStyle(document.documentElement);
    const v = (n: string, fallback: string) => css.getPropertyValue(n).trim() || fallback;

    const t = new Terminal({
      fontFamily: v("--font-mono", "monospace"),
      // Whole pixels. A fractional size gives a fractional cell width, and the
      // DOM renderer then places glyphs off the grid it is drawing the rest of
      // the screen on.
      fontSize: 13,
      lineHeight: 1.35,
      cursorBlink: true,
      // The agent owns the scrollback; ours only needs to cover a reconnect.
      scrollback: 5000,
      theme: {
        background: v("--bg-app", "#17181c"),
        foreground: v("--fg-1", "#e9eaee"),
        cursor: v("--accent", "#59c2e0"),
        selectionBackground: "rgba(255,255,255,0.18)",
      },
    });
    return t;
  }

  /**
   * The terminal is created when a session starts, not on mount.
   *
   * Until then its container is display:none, and xterm sized inside a hidden
   * element ends up with no usable geometry — it renders nothing and never
   * recovers without an explicit refit. Deferring until the pane is actually on
   * screen sidesteps that, and means an unused tab costs nothing.
   */
  async function ensureTerminal(): Promise<void> {
    if (term) {
      refit();
      return;
    }
    await tick(); // let the container become visible first
    if (!host) return;

    term = makeTerminal();
    fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);

    term.onData((data) => writeToSession(data));

    // Enter and Shift+Enter reach xterm as the same key; left alone it turns
    // both into a carriage return, so the agent can't tell "submit" from
    // "insert a line break". Intercept the key ourselves: Shift+Enter emits a
    // line feed and we suppress xterm's default (return false), while plain
    // Enter and everything else fall through untouched (return true).
    term.attachCustomKeyEventHandler((event) => {
      const action = resolveTerminalKey(event);
      if (action.kind === "default") return true;
      event.preventDefault();
      writeToSession(action.data);
      return false;
    });

    // Sizing is driven by the element, not by one measurement taken at a moment
    // we hope is the right one. Going from display:none to visible does not
    // settle within a microtask, and fitting too early yields a one-column
    // terminal that renders nothing and never recovers. The observer also covers
    // window and pane resizes, so there is a single path for all of it.
    observer = new ResizeObserver(() => scheduleRefit());
    observer.observe(host);
    refit();
  }

  function writeToSession(data: string): void {
    if (!sessionId || sessionId === PENDING) return;
    void api.author.write(sessionId, btoa(data));
  }

  /**
   * Every size we push reaches the agent as SIGWINCH, and agents respond by
   * discarding their frame and redrawing it. Dragging the window edge produces
   * an observation per frame, so pushing every one interleaves dozens of partial
   * redraws with the output still streaming in.
   *
   * Throttled on the leading edge, not debounced: xterm has already resized
   * itself by the time we are called, so until the PTY is told, the agent is
   * generating output for a width the screen does not have. Waiting for the
   * drag to settle would hold that disagreement open for as long as the drag
   * lasts. Push at once, coalesce the flood behind it, and push once more at
   * the end so the final size is never the one that got swallowed.
   */
  const refitDelay = 80;
  let refitTimer: ReturnType<typeof setTimeout> | null = null;
  let refitMissed = false;

  function scheduleRefit(): void {
    if (refitTimer) {
      refitMissed = true;
      return;
    }
    refit();
    refitTimer = setTimeout(endRefitWindow, refitDelay);
  }

  function endRefitWindow(): void {
    refitTimer = null;
    if (!refitMissed) return;
    refitMissed = false;
    scheduleRefit();
  }

  function refit(): void {
    if (!term || !fit || !host) return;
    if (host.clientWidth < 2 || host.clientHeight < 2) return; // not laid out yet
    try {
      fit.fit();
    } catch {
      // FitAddon throws if the terminal is mid-teardown; the next observation
      // will catch up.
      return;
    }
    pushSize();
  }

  /**
   * Tells the PTY how big the emulator actually is.
   *
   * Unconditionally, not only when fit() changed something: the size the PTY was
   * started with is measured before the running bar exists, and a fit that
   * happened while the id was still PENDING had nowhere to send its result. When
   * the two disagree the agent erases and positions against a width the emulator
   * does not have, which is what leaves one line's tail lying under the next.
   */
  function pushSize(): void {
    if (!term || !sessionId || sessionId === PENDING) return;
    void api.author.resize(sessionId, term.cols, term.rows);
  }

  /**
   * A hidden element (display:none) has no geometry, so xterm cannot size
   * itself while the tab is in the background. Refit once it is on screen
   * again — the ResizeObserver covers the same transition, but doing it here
   * makes returning to a live session snap back immediately rather than on the
   * next stray layout tick.
   */
  $effect(() => {
    if (active && sessionId && sessionId !== PENDING) void ensureTerminal();
  });

  /** atob gives latin-1 code units; the terminal wants the raw bytes back. */
  function writeChunk(base64: string): void {
    const bin = atob(base64);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    term?.write(bytes);
  }

  onMount(() => {
    unsubscribe.push(
      onAuthorData((ev) => {
        if (sessionId === PENDING) {
          earlyOutput.push(ev);
          return;
        }
        if (ev.sessionId !== sessionId) return;
        writeChunk(ev.data);
      }),
      onAuthorExit((ev) => {
        if (ev.sessionId !== sessionId) return;
        result = ev;
        void refresh();
        // Jump to what was just written, which is invariably what you want next.
        if (ev.outcome.created && ev.spec.prd) void selectPrd(ev.spec.prd);
      }),
    );
  });

  onDestroy(() => {
    for (const off of unsubscribe) off();
    unsubscribe = [];
    observer?.disconnect();
    if (refitTimer) clearTimeout(refitTimer);
    // Only reached when the pane itself is torn down — the app closing, not an
    // ordinary tab switch, which now leaves this component mounted and hidden.
    // Releasing the session here is the existing close-session behaviour: don't
    // orphan an agent holding a PTY with nothing attached to it.
    if (sessionId && sessionId !== PENDING && !result) void api.author.stop(sessionId);
    term?.dispose();
  });

  async function start(kind: "new" | "edit", prd?: string): Promise<void> {
    if (starting) return;
    startedFor = prd ?? conversationPrd ?? null;
    error = null;
    result = null;
    starting = true;
    try {
      // Reveal the terminal container before creating the terminal, so it is
      // measured against real geometry.
      sessionId = PENDING;
      earlyOutput = [];
      await ensureTerminal();
      // reset(), not clear(): clear() empties the screen but leaves the modes
      // behind — scroll region, alternate screen, cursor state. A session ended
      // by Stop is SIGKILLed and never emits its restore sequences, so without
      // this the next agent draws inside the dead one's scroll region.
      term?.reset();

      const id = await api.author.start({
        kind,
        prd: prd ?? conversationPrd ?? "",
        cols: term?.cols ?? 120,
        rows: term?.rows ?? 32,
      } as never);
      adoptSession(id);

      term?.focus();
    } catch (err) {
      sessionId = null;
      earlyOutput = [];
      error = errorMessage(err);
    } finally {
      starting = false;
    }
  }

  /**
   * Takes ownership of a started session.
   *
   * Both halves matter and both have to happen before anything yields, or a live
   * event overtakes the buffer it is supposed to follow: replay what arrived
   * while the id was PENDING, then push the size, because the fit triggered by
   * this pane becoming visible had no id to send its result to and the PTY is
   * still on whatever was measured before that.
   */
  function adoptSession(id: string): void {
    sessionId = id;
    for (const ev of earlyOutput) {
      if (ev.sessionId === id) writeChunk(ev.data);
    }
    earlyOutput = [];
    pushSize();
  }

  async function stop(): Promise<void> {
    if (sessionId) await api.author.stop(sessionId);
  }

  /**
   * Publishing is its own step, after the PRD has been saved. Switching tabs
   * does not cancel it — the request is owned by the store, not this view — and
   * the per-story outcome is on screen when the user comes back.
   */
  let publishing = $state(false);
  const publishLabel = $derived(
    publishing ? "Publishing…" : app.publishing?.failed?.length ? "Retry failed" : "Publish issues",
  );

  async function publish(prd: string): Promise<void> {
    if (publishing) return;
    publishing = true;
    try {
      await publishIssues(prd);
    } finally {
      publishing = false;
    }
  }

  /**
   * Opens a fresh conversation for the same PRD.
   *
   * startedFor has to be released along with the session id. It is what stops
   * the effect above from opening a second agent for a PRD that already has
   * one, and leaving it set was enough to strand the pane on "Starting the
   * conversation…" with nothing actually starting — the state you landed in
   * after stopping a session part-way through.
   */
  function reset(): void {
    // A conversation that produced stories has left a real PRD behind, so the
    // next one revises it. Starting another `new` session against it would be
    // refused, and rightly — its prompt writes the document from scratch.
    const kind = (result?.outcome.stories ?? 0) > 0 ? "edit" : (target?.kind ?? "new");
    sessionId = null;
    result = null;
    startedFor = null;
    void start(kind, conversationPrd ?? undefined);
  }
</script>

<div class="author" class:hidden={!active}>
  {#if !sessionId && conversationPrd}
    <!-- The conversation starts on its own; this is only reached when starting
         it failed, so it explains what went wrong and offers a retry. -->
    <div class="setup">
      <h2>{editing ? `Edit ${editing}` : conversationPrd}</h2>
      {#if starting}
        <p class="hint">Starting the conversation…</p>
      {:else}
        <!-- Reached when a start failed, and after a session has been released.
             Either way there is no conversation and the way back to one is a
             button, never a message that implies something is happening. -->
        {#if error}
          <p class="err">{error}</p>
        {:else}
          <p class="hint">This PRD has no conversation open.</p>
        {/if}
        <div class="actions">
          <button class="primary" onclick={() => start(target?.kind ?? "new", conversationPrd)}>
            {error ? "Try again" : "Start the conversation"}
          </button>
        </div>
      {/if}
    </div>
  {/if}

  <div class="term-wrap" class:hidden={!sessionId}>
    {#if result}
      <div class="outcome" class:bad={!result.outcome.created}>
        {#if result.outcome.created && result.outcome.parsed}
          <strong>{result.spec.prd}</strong> created with {result.outcome.stories}
          {result.outcome.stories === 1 ? "story" : "stories"}.
        {:else if result.outcome.created}
          <strong>{result.spec.prd}</strong> was written but does not parse as a PRD.
          {result.outcome.parseError}
        {:else}
          Nothing was written. The PRD was not created.
        {/if}
        <button onclick={reset}>New session</button>
      </div>

      <!-- Publishing runs only after the PRD is safely on disk. A tracker
           outage therefore costs the links, never the PRD. -->
      {#if result.outcome.created && issueDestination}
        <div class="publish">
          <div class="publish-head">
            <strong>Issues</strong>
            <button disabled={publishing} onclick={() => publish(result?.spec.prd ?? "")}>
              {publishLabel}
            </button>
          </div>
          {#if app.publishing}
            <ul class="publish-list">
              {#each app.publishing.results as r (r.storyId)}
                <li>
                  <span class="story-id tnum">{r.storyId}</span>
                  {#if r.ref}
                    <a use:external href={r.ref.url} target="_blank" rel="noreferrer"
                      >{r.ref.identifier}</a
                    >
                    {#if r.skipped}<span class="note">already created</span>{/if}
                  {:else}
                    <span class="failed">{r.error ?? "not published"}</span>
                  {/if}
                </li>
              {/each}
            </ul>
            {#if app.publishing.failed?.length}
              <p class="help">
                Retrying attempts only the {app.publishing.failed.length} story{app.publishing
                  .failed.length === 1
                  ? ""
                  : "s"} without an issue; the rest keep the issues they already have.
              </p>
            {/if}
          {/if}
        </div>
      {/if}
    {:else if running}
      <div class="running-bar">
        <span class="dot"></span>
        <span>
          {editing ? `Editing ${editing}` : `Writing ${conversationPrd}`} — type in the
          terminal.
        </span>
        <button onclick={stop}>Stop</button>
      </div>
    {/if}

    <!-- The inset lives on this wrapper, never on .term. FitAddon measures the
         element the terminal was opened on and subtracts only that element's
         own padding — so padding there is counted as usable space and the fit
         comes out too large. See the note on .term-inset below. -->
    <div class="term-inset">
      <div class="term" bind:this={host}></div>
    </div>
  </div>
</div>

<style>
  .author {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
  }
  .author.hidden {
    display: none;
  }

  .setup {
    padding: 16px 18px;
    max-width: 620px;
  }

  h2 {
    margin: 0 0 4px;
    font-size: 15px;
    font-weight: 500;
    color: var(--fg-1);
  }

  .hint {
    margin: 0 0 14px;
    color: var(--fg-3);
    font-size: 12px;
    max-width: 60ch;
  }

  .actions {
    display: flex;
    gap: 8px;
    margin: 14px 0 0;
  }

  button {
    padding: 4px 12px;
    background: var(--bg-raised);
    color: var(--fg-2);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    font: inherit;
    cursor: default;
  }
  button:hover:not(:disabled) {
    color: var(--fg-1);
    border-color: var(--fg-3);
  }
  button:disabled {
    opacity: 0.4;
  }
  .primary {
    border-color: var(--accent);
    color: var(--fg-1);
  }

  .err {
    color: var(--danger);
    font-size: 12px;
    margin: 4px 0 0;
  }

  .publish {
    padding: 8px 14px 10px;
    border-bottom: 1px solid var(--border);
    font-size: 12px;
  }
  .publish-head {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .publish-head button {
    margin-left: auto;
  }
  .publish-list {
    list-style: none;
    margin: 8px 0 0;
    padding: 0;
  }
  .publish-list li {
    display: flex;
    align-items: baseline;
    gap: 8px;
    padding: 2px 0;
  }
  .story-id {
    color: var(--fg-3);
    font-family: var(--font-mono);
  }
  /* A story that failed is called out per story, not as one verdict: the ones
     that succeeded keep their issues and must not read as lost. */
  .failed {
    color: var(--warn);
  }
  .note {
    color: var(--fg-3);
  }

  .term-wrap {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
  }
  .term-wrap.hidden {
    display: none;
  }

  /**
   * The terminal's breathing room, kept one element above the terminal itself.
   *
   * FitAddon reads its width and height from the element the terminal was
   * opened on, but subtracts padding from the terminal's own `.xterm` div. Any
   * padding on the host is therefore measured as space the terminal can use and
   * never taken back — and `box-sizing: border-box`, which app.css applies to
   * everything, folds it into the measured height rather than out of it.
   *
   * 8px/10px here was one row and two columns of overstated fit. The agent was
   * told a screen size larger than the one on show, so it drew its input box
   * below the visible area and erased past the right-hand edge: text landing on
   * top of text, and only once the screen filled and it began scrolling.
   *
   * Keep .term free of padding, borders and anything else with a box.
   */
  .term-inset {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    padding: 8px 10px;
  }

  .term {
    flex: 1;
    min-height: 0;
  }

  .running-bar,
  .outcome {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 7px 14px;
    border-bottom: 1px solid var(--border);
    font-size: 12px;
    color: var(--fg-2);
  }
  .running-bar button,
  .outcome button {
    margin-left: auto;
  }

  .outcome {
    color: var(--ok);
    border-bottom-color: var(--ok);
  }
  .outcome.bad {
    color: var(--warn);
    border-bottom-color: var(--warn);
  }

  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--accent);
    animation: pulse 1.6s ease-in-out infinite;
  }
  @keyframes pulse {
    0%,
    100% {
      opacity: 0.4;
    }
    50% {
      opacity: 1;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .dot {
      animation: none;
      opacity: 1;
    }
  }
</style>
