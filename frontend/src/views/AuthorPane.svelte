<script lang="ts">
  import { onDestroy, onMount, tick } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import "@xterm/xterm/css/xterm.css";
  import { api, onAuthorData, onAuthorExit, type AuthorExit } from "../platform";
  import {
    app,
    publishIssues,
    refresh,
    savePrdWorkflow,
    selectPrd,
  } from "../stores/app.svelte";
  import { errorMessage } from "../stores/errors";
  import { resolveTerminalKey } from "../lib/terminalInput";

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

  /**
   * The two agent selections, kept independent so changing one never moves the
   * other: the best agent for a long interactive conversation is not
   * necessarily the best one for a long unattended run.
   *
   * Both are initialised from the resolved phase defaults rather than left
   * blank — a blank meaning "whatever is configured" tells the user nothing.
   */
  let authoringAgent = $state("");
  let implementationAgent = $state("");
  let stackPerStory = $state(false);
  let issueDestination = $state("");

  /**
   * Only installed agents are offered. Listing one that is not on the machine
   * turns a wrong choice into a failure at start time rather than at choice
   * time, which is the harder failure to understand.
   */
  const installedAgents = $derived(
    (app.environment?.agents ?? []).filter((a) => a.available).map((a) => a.name),
  );

  /**
   * A saved or defaulted agent that is not installed. The selector shows it and
   * blocks the phase until it is replaced, rather than silently substituting.
   */
  function isMissing(agent: string): boolean {
    return agent !== "" && installedAgents.length > 0 && !installedAgents.includes(agent);
  }

  // Adopt the resolved defaults once they arrive, without overwriting a choice
  // the user has already made.
  $effect(() => {
    const defaults = app.agentDefaults;
    if (!defaults) return;
    if (!authoringAgent) authoringAgent = defaults.authoring;
    if (!implementationAgent) implementationAgent = defaults.implementation;
  });
  let starting = $state(false);
  let result = $state<AuthorExit | null>(null);
  let error = $state<string | null>(null);

  const nameValid = $derived(/^[A-Za-z0-9_-]+$/.test(name));
  const running = $derived(sessionId !== null && sessionId !== "pending" && result === null);

  /**
   * The PRD this pane is editing, or null when it is creating one.
   *
   * Read from the store's authorTarget rather than selectedPrd: selecting a
   * different PRD in the rail must not silently re-point a live editing session
   * at another document.
   */
  const editing = $derived(app.authorTarget.kind === "edit" ? app.authorTarget.prd : null);

  /**
   * Choosing Edit PRD in the rail is the decision — the pane does not ask again.
   * The guard is on the target, so returning to the tab or re-selecting the same
   * PRD cannot start a second session for it.
   */
  let startedFor = $state<string | null>(null);
  $effect(() => {
    const target = editing;
    if (!target || startedFor === target || sessionId) return;
    startedFor = target;
    void start("edit", target);
  });

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
    observer = new ResizeObserver(() => refit());
    observer.observe(host);
    refit();
  }

  function writeToSession(data: string): void {
    if (!sessionId || sessionId === "pending") return;
    void api.author.write(sessionId, btoa(data));
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

  async function start(kind: "new" | "edit", prd?: string): Promise<void> {
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
        prd: kind === "edit" ? (prd ?? editing ?? "") : name,
        context,
        agent: authoringAgent,
        cols: term?.cols ?? 120,
        rows: term?.rows ?? 32,
      } as never);
      sessionId = id;

      // Saved now, while the choices are in front of the user, rather than
      // after the agent writes prd.md — a session that is abandoned or closed
      // would otherwise lose them. The sidecar does not need the document to
      // exist. Saving starts nothing: no branch, no pull request, no tracker
      // write.
      if (kind === "new") {
        await savePrdWorkflow(name, {
          implementationAgent,
          stackPerStory,
          issueDestination,
        } as never);
      }
      term?.focus();
    } catch (err) {
      sessionId = null;
      error = errorMessage(err);
    } finally {
      starting = false;
    }
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

  function reset(): void {
    sessionId = null;
    result = null;
    name = "";
    context = "";
    term?.clear();
  }
</script>

<div class="author" class:hidden={!active}>
  {#if !sessionId && editing}
    <!-- The edit session starts on its own; this is only reached when starting
         it failed, so it explains what went wrong and offers a retry. -->
    <div class="setup">
      <h2>Edit {editing}</h2>
      {#if error}
        <p class="err">{error}</p>
        <div class="actions">
          <button class="primary" disabled={starting} onclick={() => start("edit", editing)}>
            {starting ? "Starting…" : "Try again"}
          </button>
        </div>
      {:else}
        <p class="hint">Starting the editing session…</p>
      {/if}
    </div>
  {:else if !sessionId}
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

      <!-- Options are grouped by phase, so which of them writes the PRD and
           which of them implements it stays visually explicit. -->
      <h3>Agents</h3>

      <label>
        <span>Authoring agent</span>
        <select bind:value={authoringAgent} aria-describedby="authoring-help">
          {#if isMissing(authoringAgent)}
            <option value={authoringAgent}>{authoringAgent} (not installed)</option>
          {/if}
          {#each installedAgents as agent (agent)}
            <option value={agent}>{agent}</option>
          {/each}
        </select>
      </label>
      <p class="help" id="authoring-help">Writes this PRD with you, now.</p>
      {#if isMissing(authoringAgent)}
        <p class="err">{authoringAgent} is not installed. Choose another agent to start.</p>
      {/if}

      <label>
        <span>Implementation agent</span>
        <select bind:value={implementationAgent} aria-describedby="implementation-help">
          {#if isMissing(implementationAgent)}
            <option value={implementationAgent}>{implementationAgent} (not installed)</option>
          {/if}
          {#each installedAgents as agent (agent)}
            <option value={agent}>{agent}</option>
          {/each}
        </select>
      </label>
      <p class="help" id="implementation-help">
        Executes the user stories later, when you start implementation. You can change it
        then.
      </p>

      <h3>Implementation workflow</h3>

      <label class="check">
        <input type="checkbox" bind:checked={stackPerStory} />
        <span class="check-label">Stack a pull request per user story</span>
      </label>
      <p class="help">
        Saved with the PRD and applied when implementation starts. Choosing it creates no
        branches or pull requests now.
      </p>

      <label>
        <span>Publish issues to</span>
        <select bind:value={issueDestination}>
          <option value="">Do not publish</option>
          {#each app.destinations as dest (dest.destination)}
            <option value={dest.destination} disabled={!dest.available}>
              {dest.name}{dest.available ? "" : " — unavailable"}
            </option>
          {/each}
        </select>
      </label>
      {#each app.destinations.filter((d) => !d.available && d.reason) as dest (dest.destination)}
        <p class="help">{dest.name}: {dest.reason}</p>
      {/each}

      <div class="actions">
        <button
          class="primary"
          disabled={!nameValid || starting || isMissing(authoringAgent)}
          onclick={() => start("new")}
        >
          {starting ? "Starting…" : "Create"}
        </button>
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
                    <a href={r.ref.url} target="_blank" rel="noreferrer">{r.ref.identifier}</a>
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
        <!-- Naming the PRD here is what distinguishes two editing sessions from
             one another; the tab title alone says only "Edit PRD". -->
        <span>{editing ? `Editing ${editing}` : "Session live"} — type in the terminal.</span>
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

  h3 {
    margin: 18px 0 8px;
    font-size: 12px;
    font-weight: 500;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--fg-3);
  }

  /* Helper text sits under its control, aligned with the field rather than the
     label, so the phase each agent belongs to reads at a glance. */
  .help {
    margin: -4px 0 10px 162px;
    font-size: 11.5px;
    color: var(--fg-3);
    max-width: 52ch;
  }

  select {
    flex: 1;
    padding: 5px 8px;
    background: var(--bg-raised);
    color: var(--fg-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    font: inherit;
  }

  label.check {
    align-items: center;
  }
  label.check input {
    flex: none;
    margin-left: 150px;
  }
  .check-label {
    width: auto;
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
