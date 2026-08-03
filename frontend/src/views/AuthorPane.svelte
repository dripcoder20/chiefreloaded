<script lang="ts">
  import { onDestroy, onMount, tick } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import "@xterm/xterm/css/xterm.css";
  import { api, onAuthorData, onAuthorExit, type AuthorExit } from "../platform";
  import { app, refresh, selectPrd } from "../stores/app.svelte";

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

  let sessionId = $state<string | null>(null);
  let name = $state("");
  let context = $state("");
  let starting = $state(false);
  let result = $state<AuthorExit | null>(null);
  let error = $state<string | null>(null);

  const nameValid = $derived(/^[A-Za-z0-9_-]+$/.test(name));
  const running = $derived(sessionId !== null && sessionId !== "pending" && result === null);

  function makeTerminal(): Terminal {
    const css = getComputedStyle(document.documentElement);
    const v = (n: string, fallback: string) => css.getPropertyValue(n).trim() || fallback;

    const t = new Terminal({
      fontFamily: v("--font-mono", "monospace"),
      fontSize: 12.5,
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

    term.onData((data) => {
      if (!sessionId || sessionId === "pending") return;
      void api.author.write(sessionId, btoa(data));
    });

    // Sizing is driven by the element, not by one measurement taken at a moment
    // we hope is the right one. Going from display:none to visible does not
    // settle within a microtask, and fitting too early yields a one-column
    // terminal that renders nothing and never recovers. The observer also covers
    // window and pane resizes, so there is a single path for all of it.
    observer = new ResizeObserver(() => refit());
    observer.observe(host);
    refit();
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
    if (sessionId && sessionId !== "pending") {
      void api.author.resize(sessionId, term.cols, term.rows);
    }
  }

  /**
   * A hidden element (display:none) has no geometry, so xterm cannot size
   * itself while the tab is in the background. Refit once it is on screen
   * again — the ResizeObserver covers the same transition, but doing it here
   * makes returning to a live session snap back immediately rather than on the
   * next stray layout tick.
   */
  $effect(() => {
    if (active && sessionId && sessionId !== "pending") void ensureTerminal();
  });

  onMount(() => {
    unsubscribe.push(
      onAuthorData((ev) => {
        if (ev.sessionId !== sessionId) return;
        // atob gives latin-1 code units; the terminal wants the raw bytes back.
        const bin = atob(ev.data);
        const bytes = new Uint8Array(bin.length);
        for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
        term?.write(bytes);
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
    // Only reached when the pane itself is torn down — the app closing, not an
    // ordinary tab switch, which now leaves this component mounted and hidden.
    // Releasing the session here is the existing close-session behaviour: don't
    // orphan an agent holding a PTY with nothing attached to it.
    if (sessionId && sessionId !== "pending" && !result) void api.author.stop(sessionId);
    term?.dispose();
  });

  async function start(kind: "new" | "edit"): Promise<void> {
    error = null;
    result = null;
    starting = true;
    try {
      // Reveal the terminal container before creating the terminal, so it is
      // measured against real geometry.
      sessionId = "pending";
      await ensureTerminal();
      term?.clear();

      const id = await api.author.start({
        kind,
        prd: kind === "edit" ? (app.selectedPrd ?? "") : name,
        context,
        cols: term?.cols ?? 120,
        rows: term?.rows ?? 32,
      } as never);
      sessionId = id;
      term?.focus();
    } catch (err) {
      sessionId = null;
      error = String(err);
    } finally {
      starting = false;
    }
  }

  async function stop(): Promise<void> {
    if (sessionId) await api.author.stop(sessionId);
  }

  function reset(): void {
    sessionId = null;
    result = null;
    name = "";
    context = "";
    term?.clear();
  }
</script>

<div class="author" class:hidden={!active}>
  {#if !sessionId}
    <div class="setup">
      <h2>Create a PRD</h2>
      <p class="hint">
        Runs your agent interactively. It uses the prompt from Settings, so anything you
        put there — including your own slash commands — applies to every new PRD.
      </p>

      <label>
        <span>Name</span>
        <input
          type="text"
          bind:value={name}
          placeholder="checkout"
          spellcheck="false"
          onkeydown={(e) => e.key === "Enter" && nameValid && start("new")}
        />
      </label>
      {#if name && !nameValid}
        <p class="err">Letters, digits, hyphen and underscore only — it becomes a directory and a branch name.</p>
      {/if}

      <label class="tall">
        <span>What are you building?</span>
        <textarea
          bind:value={context}
          rows="4"
          placeholder="Optional. Leave blank and the agent will ask."
        ></textarea>
      </label>

      <div class="actions">
        <button class="primary" disabled={!nameValid || starting} onclick={() => start("new")}>
          {starting ? "Starting…" : "Create"}
        </button>
        {#if app.selectedPrd}
          <button disabled={starting} onclick={() => start("edit")}>
            Edit {app.selectedPrd}
          </button>
        {/if}
      </div>

      {#if error}<p class="err">{error}</p>{/if}
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
    {:else if running}
      <div class="running-bar">
        <span class="dot"></span>
        <span>Session live — type in the terminal.</span>
        <button onclick={stop}>Stop</button>
      </div>
    {/if}

    <div class="term" bind:this={host}></div>
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

  label {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 8px;
  }
  label.tall {
    align-items: flex-start;
  }
  label span {
    width: 150px;
    flex: none;
    color: var(--fg-2);
  }

  input,
  textarea {
    flex: 1;
    padding: 5px 8px;
    background: var(--bg-raised);
    color: var(--fg-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    font: inherit;
    resize: vertical;
  }
  input {
    font-family: var(--font-mono);
  }

  .actions {
    display: flex;
    gap: 8px;
    margin: 14px 0 0 162px;
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
    margin: 4px 0 0 162px;
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

  .term {
    flex: 1;
    min-height: 0;
    padding: 8px 10px;
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
