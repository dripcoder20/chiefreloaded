<script lang="ts">
  import { onMount } from "svelte";
  import {
    adjustBudget,
    answerQuestion,
    app,
    cancelDeletePrd,
    connect,
    deletePrd,
    disconnect,
    pauseRun,
    resumeRun,
    pickProject,
    toggleSettings,
    closeSettings,
    startRun,
    stopRun,
  } from "./stores/app.svelte";
  import { api } from "./platform";
  import PrdRail from "./shell/PrdRail.svelte";
  import Inspector from "./shell/Inspector.svelte";
  import StoryList from "./views/StoryList.svelte";
  import LogPanel from "./views/LogPanel.svelte";
  import SettingsDialog from "./views/SettingsDialog.svelte";
  import AuthorPane from "./views/AuthorPane.svelte";
  import UsageBar from "./views/UsageBar.svelte";

  onMount(() => {
    void connect();
    return disconnect;
  });

  let logPanel = $state<ReturnType<typeof LogPanel> | null>(null);

  const run = $derived(app.currentRun);
  // The state to display comes from the store, which folds an in-flight
  // transition over the authoritative session state.
  const runState = $derived(app.displayState);
  const transitioning = $derived(app.currentTransition);

  const startLabel = $derived(
    transitioning === "starting"
      ? "Starting…"
      : transitioning === "resuming"
        ? "Resuming…"
        : app.canResume
          ? "Resume"
          : "Start",
  );
  // A conversational editing session is never labelled "New PRD" — the tab title
  // follows what the pane is actually for.
  const authorTabLabel = $derived(app.authorTarget.kind === "edit" ? "Edit PRD" : "New PRD");

  const pauseLabel = $derived(transitioning === "pausing" ? "Pausing…" : "Pause");

  /**
   * The agent this run will use. Seeded from the PRD's saved choice — resolved
   * on the Go side, so it is the real agent rather than a default standing in
   * for one — and changeable until Start is pressed.
   */
  let startAgent = $state("");
  const installedAgents = $derived(
    (app.environment?.agents ?? []).filter((a) => a.available).map((a) => a.name),
  );

  // Re-seed whenever the selected PRD changes, so the toolbar shows that PRD's
  // agent rather than one carried over from the PRD looked at before it.
  $effect(() => {
    const prd = app.selectedPrd;
    if (!prd) return;
    void loadStartAgent(prd);
  });

  async function loadStartAgent(prd: string): Promise<void> {
    const workflow = await api.prd.workflow(prd).catch(() => null);
    startAgent = workflow?.implementationAgent || app.agentDefaults?.implementation || "";
  }
  const stopLabel = $derived(transitioning === "stopping" ? "Stopping…" : "Stop");

  const elapsed = $derived.by(() => {
    if (!run?.startedAt) return "";
    const end = run.finishedAt || app.now;
    const secs = Math.max(0, Math.floor((end - run.startedAt) / 1000));
    const m = Math.floor(secs / 60);
    const s = secs % 60;
    return `${m}:${String(s).padStart(2, "0")}`;
  });

  /**
   * Keyboard map, carried over from the TUI so muscle memory transfers.
   *
   * Single letters are suppressed while a text field has focus — otherwise
   * typing "s" in an input would start the loop, which is the classic way a
   * keyboard-driven GUI becomes hostile.
   */
  function onKey(e: KeyboardEvent): void {
    const el = e.target as HTMLElement | null;
    const editing =
      el &&
      (el.tagName === "INPUT" ||
        el.tagName === "TEXTAREA" ||
        el.isContentEditable);
    if (editing || e.metaKey || e.ctrlKey || e.altKey) return;

    switch (e.key) {
      case "s":
        app.canResume ? void resumeRun() : void startRun();
        break;
      case "p":
        void pauseRun();
        break;
      case "x":
        void stopRun();
        break;
      case "t":
        logPanel?.toggle();
        break;
      case ",":
        toggleSettings();
        break;
      case "n":
        app.view = app.view === "author" ? "stories" : "author";
        break;
      case "+":
      case "=":
        void adjustBudget(5);
        break;
      case "-":
      case "_":
        void adjustBudget(-5);
        break;
      case "j":
      case "k": {
        const stories = app.detail?.stories ?? [];
        if (stories.length === 0) break;
        const i = stories.findIndex((s) => s.id === app.selectedStory);
        const next = e.key === "j" ? i + 1 : i - 1;
        if (next >= 0 && next < stories.length) app.selectedStory = stories[next].id;
        break;
      }
      default:
        return;
    }
    e.preventDefault();
  }
</script>

<svelte:window onkeydown={onKey} />

<div class="shell">
  <header class="titlebar">
    <span class="brand">loop</span>
    {#if app.project}
      <span class="path">{app.project.name}</span>
    {/if}
  </header>

  <!-- The one continuously animating element in the app. Still means nothing is
       running; you can tell from across the room. -->
  <div class="runbar" class:active={app.anyRunning} aria-hidden="true"></div>

  {#if app.questions.length > 0}
    {@const q = app.questions[0]}
    <!-- A question blocks its run, so it belongs above everything else. -->
    <div class="question" role="alertdialog" aria-label={q.title}>
      <div>
        <strong>{q.title}</strong>
        {#if q.body}<p>{q.body}</p>{/if}
      </div>
      <div class="options">
        {#each q.options as o}
          <button
            class:recommended={o.recommended}
            class:destructive={o.destructive}
            onclick={() => answerQuestion(q.id, o.id)}
            title={o.hint}
          >
            {o.label}
          </button>
        {/each}
      </div>
    </div>
  {/if}

  {#if app.pendingDelete}
    {@const target = app.pendingDelete}
    <!-- Deleting removes a directory of work, so the dialog names its target and
         nothing is touched until Delete is chosen. -->
    <div class="question" role="alertdialog" aria-label="Delete PRD {target}">
      <div>
        <strong>Delete “{target}”?</strong>
        <p>Its PRD, progress notes and agent logs are removed. This cannot be undone.</p>
      </div>
      <div class="options">
        <button onclick={cancelDeletePrd}>Cancel</button>
        <button class="destructive" onclick={() => deletePrd(target)}>Delete PRD</button>
      </div>
    </div>
  {/if}

  {#if app.settingsOpen}
    <SettingsDialog onclose={closeSettings} />
  {/if}

  {#if app.error}
    <div class="error" role="alert">
      {app.error}
      <button onclick={() => (app.error = null)} aria-label="Dismiss">×</button>
    </div>
  {/if}

  <div class="body">
    <PrdRail />

    <main class="centre">
      <div class="toolbar">
        <span class="badge {runState}">{runState}</span>

        {#if run}
          <span class="tnum meta">attempt {run.attempt}/{run.attemptBudget}</span>
          {#if elapsed}<span class="tnum meta">{elapsed}</span>{/if}
          {#if run.pendingGitErrors > 0}
            <!-- Non-fatal but must not be missable: the run continued, but
                 pull requests did not get opened. -->
            <span class="git-warn">
              {run.pendingGitErrors} git action{run.pendingGitErrors === 1 ? "" : "s"} need attention
            </span>
          {/if}
        {/if}

        <span class="spacer"></span>

        <!-- The implementation agent is changeable right up to the moment the
             run is confirmed, which is the last point at which it still matters. -->
        {#if app.canStart}
          <label class="agent-pick">
            <span class="sr-only">Implementation agent</span>
            <select bind:value={startAgent} aria-label="Implementation agent">
              {#each installedAgents as agent (agent)}
                <option value={agent}>{agent}</option>
              {/each}
            </select>
          </label>
        {/if}

        <button
          onclick={() => (app.canResume ? resumeRun() : startRun(startAgent))}
          disabled={!app.canStart && !app.canResume}
        >
          {startLabel}
        </button>
        <button onclick={pauseRun} disabled={!app.canPause}>{pauseLabel}</button>
        <button onclick={stopRun} disabled={!app.canStop}>{stopLabel}</button>
      </div>

      <div class="tabs" role="tablist">
        <button
          role="tab"
          aria-selected={app.view === "stories"}
          class:on={app.view === "stories"}
          onclick={() => (app.view = "stories")}>Stories</button
        >
        <button
          role="tab"
          aria-selected={app.view === "author"}
          class:on={app.view === "author"}
          onclick={() => (app.view = "author")}>{authorTabLabel}</button
        >
      </div>

      <div class="pane">
        <!-- Kept mounted across tab switches so an active authoring session,
             its terminal and its unsent input survive leaving the tab. It hides
             itself when it is not the active view. -->
        <AuthorPane active={app.view === "author"} />

        {#if app.view === "stories"}
          {#if !app.project || app.prds.length === 0}
            <div class="blank">
              <p>
                {#if !app.project}
                  No project open.
                {:else if !app.project.hasChiefDir}
                  <strong>{app.project.name}</strong> has no <code>.chief/</code> directory yet.
                {:else}
                  <strong>{app.project.name}</strong> has no PRDs yet.
                {/if}
              </p>
              <p class="hint">
                Loop opens the directory it was launched from.
              </p>
              <div class="blank-actions">
                <button class="primary" onclick={() => (app.view = "author")}>Create a PRD</button>
                <button onclick={pickProject}>Choose a project…</button>
              </div>
            </div>
          {:else}
            <StoryList />
          {/if}
        {/if}
      </div>

      <!-- Always present, not a view you switch to: watching the agent and
           watching the stories tick over are the same activity. -->
      <LogPanel bind:this={logPanel} />
    </main>

    <Inspector />
  </div>

  <footer class="statusbar">
    <span class="activity">{app.activity}</span>
    <span class="spacer"></span>
    <UsageBar run={run} session={app.currentUsage.session} story={app.currentUsage.story} />
    {#if app.runningCount > 0}
      <span class="tnum">{app.runningCount} running</span>
    {/if}
    <span class="keys">s start · p pause · x stop · t log · n new PRD · , settings · j/k move</span>
  </footer>
</div>

<style>
  .shell {
    display: flex;
    flex-direction: column;
    height: 100vh;
    overflow: hidden;
  }

  .titlebar {
    display: flex;
    align-items: center;
    gap: 12px;
    height: 38px;
    padding-left: 84px; /* clear of the macOS traffic lights */
    border-bottom: 1px solid var(--border);
    --wails-draggable: drag;
  }

  .brand {
    font: 500 13px var(--font-mono);
    color: var(--fg-1);
    letter-spacing: -0.01em;
  }
  .path {
    color: var(--fg-3);
  }

  .runbar {
    height: 2px;
    background: transparent;
    flex: none;
  }
  .runbar.active {
    background: linear-gradient(90deg, transparent, var(--accent), transparent);
    background-size: 40% 100%;
    background-repeat: no-repeat;
    animation: sweep 1.8s linear infinite;
  }
  @keyframes sweep {
    from {
      background-position: -40% 0;
    }
    to {
      background-position: 140% 0;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .runbar.active {
      animation: none;
      background: var(--accent);
    }
  }

  .body {
    display: flex;
    flex: 1;
    min-height: 0;
  }

  .centre {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
    position: relative;
  }

  .toolbar,
  .tabs {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 14px;
    border-bottom: 1px solid var(--border);
  }
  .tabs {
    gap: 4px;
    padding: 0 10px;
  }

  .pane {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
  }

  .badge {
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    padding: 2px 7px;
    border-radius: 3px;
    background: var(--bg-raised);
    color: var(--fg-3);
  }
  .badge.running,
  .badge.starting,
  .badge.resuming {
    color: var(--accent);
  }
  .badge.pausing,
  .badge.stopping {
    color: var(--warn);
  }
  .badge.complete {
    color: var(--ok);
  }
  .badge.paused {
    color: var(--warn);
  }
  .badge.error {
    color: var(--danger);
  }

  .meta {
    color: var(--fg-3);
    font-size: 12px;
  }

  .git-warn {
    color: var(--warn);
    font-size: 12px;
  }

  /* Kept low-weight: it sits next to Start but must not compete with it. */
  .agent-pick select {
    padding: 3px 6px;
    background: var(--bg-raised);
    color: var(--fg-2);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    font: inherit;
    font-size: 11.5px;
  }

  /* Visually hidden but announced, so the control has a name without a visible
     label crowding the toolbar. */
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }

  .spacer {
    flex: 1;
  }

  button {
    padding: 3px 10px;
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

  .tabs button {
    background: none;
    border: 0;
    border-bottom: 2px solid transparent;
    border-radius: 0;
    padding: 7px 10px;
    color: var(--fg-3);
  }
  .tabs button.on {
    color: var(--fg-1);
    border-bottom-color: var(--accent);
  }

  .statusbar {
    display: flex;
    align-items: center;
    gap: 14px;
    height: 26px;
    padding: 0 14px;
    border-top: 1px solid var(--border);
    font-size: 11.5px;
    color: var(--fg-3);
    flex: none;
  }

  .activity {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 55%;
    font-family: var(--font-mono);
  }

  .keys {
    font-family: var(--font-mono);
    opacity: 0.7;
  }

  .blank {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 10px;
    flex: 1;
    color: var(--fg-3);
    text-align: center;
    padding: 0 32px;
  }
  .blank p {
    margin: 0;
    max-width: 46ch;
  }
  .blank .hint {
    font-size: 12px;
  }
  .blank-actions {
    display: flex;
    gap: 8px;
  }
  .blank-actions .primary {
    border-color: var(--accent);
    color: var(--fg-1);
  }

  .blank code {
    font-family: var(--font-mono);
    background: var(--bg-raised);
    padding: 0 4px;
    border-radius: 3px;
  }

  .question {
    display: flex;
    align-items: center;
    gap: 16px;
    padding: 10px 14px;
    background: var(--bg-raised);
    border-bottom: 1px solid var(--warn);
  }
  .question p {
    margin: 2px 0 0;
    color: var(--fg-2);
  }
  .options {
    display: flex;
    gap: 6px;
    margin-left: auto;
  }
  .options .recommended {
    border-color: var(--accent);
    color: var(--fg-1);
  }
  .options .destructive {
    border-color: var(--danger);
    color: var(--danger);
  }

  .error {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 14px;
    background: color-mix(in srgb, var(--danger) 14%, transparent);
    border-bottom: 1px solid var(--danger);
    color: var(--danger);
  }
  .error button {
    margin-left: auto;
    border: 0;
    background: none;
    color: inherit;
    font-size: 16px;
    line-height: 1;
  }
</style>
