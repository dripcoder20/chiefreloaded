import {
  api,
  isLogEvent,
  EventKind,
  LoopState,
  onEvents,
  onMenuNewPRD,
  onMenuOpenProject,
  onMenuSettings,
  onReady,
  type AgentDefaults,
  type AppStatus,
  type Environment,
  type NewPRDRequest,
  type PRDWorkflow,
  type PublishOffer,
  type PublishReport,
  type StackReport,
  type Settings,
  type LoopEvent,
  type PRDDetail,
  type PRDSummary,
  type Project,
  type Question,
  type RunSnapshot,
} from "../platform";
import { ingest } from "./logs.svelte";
import { errorMessage } from "./errors";

/**
 * Application state.
 *
 * Everything is derived from two sources: a Snapshot taken at startup, and the
 * event stream thereafter. When the stream reports a gap the snapshot is simply
 * retaken — that is the whole recovery story, and it is why the Go side bothers
 * to sequence-number events.
 */

// Settings is deliberately absent: it is global project configuration, not a
// per-PRD working context like the others, and it opens as a dialog.
export type View = "stories" | "summary" | "prd-settings" | "author";

/**
 * A control request that has been sent but not yet resolved.
 *
 * The authoritative session state lives on the Go side; between firing a
 * request and the backend confirming the new state there is a window in which
 * the last-known snapshot is stale. Recording the in-flight transition — keyed
 * by PRD so it survives switching away and back — lets the UI show `Starting`,
 * `Pausing`, etc. and refuse a duplicate of the same action for that PRD.
 */
export type Transition = "starting" | "pausing" | "resuming" | "stopping";

/**
 * Usage read model and its pure presentation logic now live in ./usage, which
 * imports nothing from ../platform so it is unit-testable without the generated
 * Wails bindings. Re-exported here so components keep importing from one place.
 */
export {
  DEFAULT_THRESHOLDS,
  contextUtilization,
  contextLevel,
  worstContext,
  costLevel,
  usagePhase,
  hasCost,
  isMixedProvider,
  isPartial,
  historyIsEmpty,
  usageErrorMessage,
} from "./usage";
export type {
  UsageGroup,
  UsageTotals,
  StoryUsage,
  SessionUsage,
  UsageReport,
  UsageThresholds,
  WarnLevel,
  UsagePhase,
} from "./usage";

export {
  isImplementing,
  metaValue,
  metaText,
  storyMeta,
  RESOLVING_LABEL,
  UNAVAILABLE_LABEL,
} from "./sessionMeta";
export type { MetaValue, StoryMeta, SessionMetaRun } from "./sessionMeta";


import type {
  UsageReport,
  UsageTotals,
  SessionUsage,
  StoryUsage,
  UsageThresholds,
} from "./usage";
import { DEFAULT_THRESHOLDS, usageErrorMessage } from "./usage";

class AppState {
  project = $state<Project | null>(null);
  prds = $state<PRDSummary[]>([]);
  runs = $state<RunSnapshot[]>([]);
  questions = $state<Question[]>([]);
  config = $state<Settings | null>(null);

  /** In-flight control transitions, keyed by PRD name. */
  pending = $state<Record<string, Transition>>({});

  selectedPrd = $state<string | null>(null);
  selectedStory = $state<string | null>(null);
  view = $state<View>("stories");
  detail = $state<PRDDetail | null>(null);

  /**
   * Absolute cumulative usage roll-up, adopted wholesale from the snapshot and
   * replaced by each EvUsage event's report. Never a delta, so a replayed or
   * reconnected event is idempotent.
   */
  usage = $state<UsageReport | null>(null);

  /**
   * What the authoring tab is for: writing a new PRD, or revising a named one.
   *
   * It is what the tab is titled from, so an editing session is never labelled
   * `New PRD`, and what tells the pane which PRD to start an edit session
   * against — independent of `selectedPrd`, which the rail changes for reasons
   * that have nothing to do with the open session.
   */
  authorTarget = $state<{ kind: "new" | "edit"; prd: string } | null>(null);

  /** True while the settings dialog is open. */
  settingsOpen = $state(false);

  /** True while the New PRD dialog is open. */
  newPrdOpen = $state(false);

  /**
   * PRDs with a live authoring conversation, by name.
   *
   * The conversation is a per-PRD surface, so the tab strip shows it only for
   * the PRD it belongs to. Keyed by name rather than held as one flag because
   * more than one PRD can be mid-conversation.
   */
  authoring = $state<Record<string, boolean>>({});

  /** The PRD a delete confirmation is currently asking about, if any. */
  pendingDelete = $state<string | null>(null);

  /**
   * Whether the selected PRD may be published, as the Go side decides it.
   *
   * Held rather than derived: it depends on git mode and on whether any story
   * has committed, neither of which the frontend can see. Null means the answer
   * has not arrived yet, which is also when the control stays absent — an item
   * that appears and then vanishes is worse than one that arrives late.
   */
  publishOffer = $state<PublishOffer | null>(null);

  /** True while a publish is in flight, so one press publishes once. */
  publishing = $state(false);

  /** What the last publish produced, so the pull request can be shown with a link. */
  published = $state<PublishReport | null>(null);

  /**
   * What the last stacked publish produced, per story.
   *
   * Held separately from `published`: they describe different things, and a
   * stack's outcome is a list — including the stories that got no pull request —
   * which a single report cannot carry.
   */
  publishedStack = $state<StackReport | null>(null);

  /**
   * Publishing is offered only where it can work, and never while a run for the
   * PRD is live — the engine refuses that, and offering it anyway would make the
   * control a way to read an error message.
   */
  get canPublish(): boolean {
    if (!this.selectedPrd || this.publishing) return false;
    if (isActive(this.currentRun?.state)) return false;
    return this.publishOffer?.available === true;
  }

  /**
   * A stack is offered only where the run produced one. The engine decides that
   * from the recorded layout; where it says no, the reason is shown rather than
   * the item, so the absence is explained instead of noticed.
   */
  get canPublishStack(): boolean {
    return this.canPublish && this.publishOffer?.stacked === true;
  }

  /**
   * The detected tooling, including which agent CLIs are installed. The agent
   * selectors list only these: offering an agent that is not on the machine
   * produces a failure at start time instead of at choice time.
   */
  environment = $state<Environment | null>(null);

  /** The resolved per-phase agent defaults, for the New PRD selectors. */
  agentDefaults = $state<AgentDefaults | null>(null);
  /** Which local editors are installed, for the repository launchers. */
  localApps = $state<AppStatus[]>([]);
  /** True while a repository launch is in flight, so one click launches once. */
  launching = $state(false);

  /** Most recent agent output, for the status bar ticker. */
  activity = $state("");
  error = $state<string | null>(null);
  connected = $state(false);
  /** Ticks once a second so elapsed timers re-render without one timer each. */
  now = $state(Date.now());

  /**
   * The latest run of the selected PRD, if any.
   *
   * Latest, not first: a PRD accumulates a run for every Start, and the
   * controls describe the session you are in. Taking the first match left Stop
   * disabled after start, stop, start — it was reading the stopped run while
   * the new one was underway. Position cannot be trusted either, since the Go
   * side holds runs in a map and the list arrives in no particular order, so
   * the answer comes from when each run started.
   */
  get currentRun(): RunSnapshot | null {
    const prd = this.selectedPrd;
    if (!prd) return null;
    return this.runs
      .filter((r) => r.prd === prd)
      .reduce<RunSnapshot | null>(
        (latest, run) => (!latest || (run.startedAt ?? 0) >= (latest.startedAt ?? 0) ? run : latest),
        null,
      );
  }

  get currentPrd(): PRDSummary | null {
    return this.prds.find((p) => p.name === this.selectedPrd) ?? null;
  }

  /** The unresolved control transition for the selected PRD, if any. */
  get currentTransition(): Transition | null {
    const prd = this.selectedPrd;
    if (!prd) return null;
    return this.pending[prd] ?? null;
  }

  /**
   * What to show for the selected PRD. An in-flight transition wins over the
   * last-known authoritative state so the UI never displays a stale `idle`
   * while a Start is still resolving.
   */
  get displayState(): string {
    return this.currentTransition ?? this.currentRun?.state ?? LoopState.StateIdle;
  }

  /** Controls are derived from the authoritative state, never from the last
   *  request — a transition in flight disables every control for that PRD. */
  get canStart(): boolean {
    return !this.currentTransition && isStartable(this.currentRun?.state);
  }

  get canResume(): boolean {
    return !this.currentTransition && this.currentRun?.state === LoopState.StatePaused;
  }

  get canPause(): boolean {
    return !this.currentTransition && this.currentRun?.state === LoopState.StateRunning;
  }

  get canStop(): boolean {
    return !this.currentTransition && isActive(this.currentRun?.state);
  }

  /** True while any PRD is running — drives the one animating element. */
  get anyRunning(): boolean {
    return this.runs.some((r) => r.state === "running");
  }

  get runningCount(): number {
    return this.runs.filter((r) => r.state === "running").length;
  }

  /**
   * The session (per-run) and current-story usage slices for the selected PRD's
   * run. Keyed exactly as the Go ledger keys them: run id, and run id + "/" +
   * story id. Undefined slices mean no usage has been attributed there yet.
   */
  get currentUsage(): { session?: UsageTotals; story?: UsageTotals } {
    const run = this.currentRun;
    if (!run || !this.usage) return {};
    const story = run.storyId ? this.usage.stories[`${run.id}/${run.storyId}`] : undefined;
    return { session: this.usage.runs[run.id], story };
  }

  /** The project (General) usage grand total across every run. */
  get generalUsage(): UsageTotals | undefined {
    return this.usage?.project;
  }

  /**
   * Every story this run has spent usage on, with its own totals and attempt
   * count — not just the one executing.
   *
   * The report already carries this: the session history folds each run's
   * stories from the same records. Showing only the current story made the
   * Story scope useless the moment a run moved on.
   */
  get currentRunStories(): StoryUsage[] {
    const run = this.currentRun;
    if (!run) return [];
    return this.usage?.sessions?.find((s) => s.runId === run.id)?.stories ?? [];
  }

  /** The retained-session history, newest first, for the General scope. */
  get generalSessions(): SessionUsage[] {
    return this.usage?.sessions ?? [];
  }

  /**
   * The configured usage-warning thresholds, falling back to the defaults when a
   * project (or its usage block) is not loaded. Reads $state so it stays reactive
   * when settings change.
   */
  get thresholds(): UsageThresholds {
    const u = (this.config as { usage?: Partial<UsageThresholds> } | null)?.usage;
    return {
      contextWarnPercent: u?.contextWarnPercent || DEFAULT_THRESHOLDS.contextWarnPercent,
      contextCriticalPercent:
        u?.contextCriticalPercent || DEFAULT_THRESHOLDS.contextCriticalPercent,
      costWarnAmount: u?.costWarnAmount ?? undefined,
    };
  }

  /** The human title of the run's current story, for naming a scope. */
  get currentStoryTitle(): string | null {
    const run = this.currentRun;
    if (!run?.storyId) return null;
    const story = this.detail?.stories?.find((s) => s.id === run.storyId);
    return story?.title ?? null;
  }
}

export const app = new AppState();

let unsubscribe: Array<() => void> = [];

/** Wire up the event stream and take the first snapshot. */
export async function connect(): Promise<void> {
  unsubscribe.push(onReady(() => void refresh()));
  unsubscribe.push(onMenuNewPRD(requestNewPRD));
  unsubscribe.push(onMenuOpenProject(() => void pickProject()));
  unsubscribe.push(onMenuSettings(toggleSettings));
  unsubscribe.push(
    onEvents((events) => {
      for (const ev of events) apply(ev);
      // Log events go to the ring buffer wholesale; it does its own batching.
      ingest(events.filter((e) => isLogEvent(e.kind)));
    }),
  );

  setInterval(() => (app.now = Date.now()), 1000);
  await refresh();
  void reloadLocalApps();
  void reloadCreationOptions();
  app.connected = true;
}

export function disconnect(): void {
  for (const off of unsubscribe) off();
  unsubscribe = [];
}

/** Adopt a fresh snapshot. The recovery path for any gap in the stream. */
export async function refresh(): Promise<void> {
  try {
    const snap = await api.run.snapshot();
    app.project = snap.project ?? null;
    app.prds = snap.prds ?? [];
    app.runs = snap.runs ?? [];
    app.questions = snap.questions ?? [];
    app.usage = (snap as { usage?: UsageReport }).usage ?? null;
    app.environment = snap.environment ?? null;

    if (!app.selectedPrd && app.prds.length > 0) {
      await selectPrd(app.prds[0].name);
    }
    if (!app.config) {
      app.config = await api.project.getConfig();
    }
  } catch (err) {
    app.error = errorMessage(err);
  }
}

/** Apply one event to the in-memory model. */
function apply(ev: LoopEvent): void {
  // A gap means we cannot trust our derived state, so rebuild it wholesale
  // rather than trying to patch around the hole.
  if (ev.dropped) {
    void refresh();
  }

  switch (ev.kind) {
    case EventKind.EvProjectOpened:
    case EventKind.EvPRDChanged:
    case EventKind.EvProgressChange:
      void reloadPrds();
      break;

    case EventKind.EvConfigChanged:
      void api.project.getConfig().then((c) => (app.config = c));
      break;

    case EventKind.EvRunStarted:
    case EventKind.EvRunPaused:
    case EventKind.EvRunResumed:
    case EventKind.EvRunStopped:
    case EventKind.EvRunComplete:
    case EventKind.EvRunError:
    // A run whose state has not changed but whose metadata has — the agent
    // resolving which model it is running.
    case EventKind.EvRunUpdated:
      void reloadRuns();
      if (ev.kind === EventKind.EvRunError) app.error = ev.text ?? "the run failed";
      if (ev.kind === EventKind.EvRunComplete) app.activity = "complete";
      break;

    case EventKind.EvStoryStarted:
      app.activity = ev.text ? `${ev.storyId}: ${ev.text}` : (ev.storyId ?? "");
      // The engine writes the story's in-progress status into prd.md as it
      // builds the prompt, and nothing watches that file. Without this the
      // story list only catches up when a story finishes, so the one being
      // worked on never looks like it.
      void reloadPrds();
      break;

    case EventKind.EvStoryDone:
    case EventKind.EvStorySkipped:
    case EventKind.EvStoryFailed:
      void reloadPrds();
      break;

    case EventKind.EvGit:
      // A branch was cut or a pull request opened, so what the interface knows
      // about both is now out of date. Reloading the PRDs picks up the branch
      // the engine just recorded; the pull request is confirmed separately
      // because that one costs a call to gh.
      void reloadPrds();
      if (ev.prd && ev.git?.prNumber) void refreshPullRequests(ev.prd);
      break;

    case EventKind.EvAgentText:
      app.activity = truncate(ev.text ?? "", 120);
      break;

    case EventKind.EvAgentTool:
      app.activity = `${ev.agent?.tool ?? "tool"} ${toolArgument(ev)}`.trim();
      break;

    case EventKind.EvQuestionAsked:
    case EventKind.EvQuestionResolved:
      void api.run.questions().then((q) => (app.questions = q));
      break;

    case EventKind.EvUsage: {
      // The report is an absolute roll-up, so adopt it wholesale; replay and
      // reconnect both re-adopt the same numbers rather than adding.
      const report = (ev as { usageReport?: UsageReport }).usageReport;
      if (report) app.usage = report;
      break;
    }

    case EventKind.EvUsageError:
      // Persisted history failed to load. Surface it — live usage is
      // unaffected — rather than leaving the failure silent.
      app.error = usageErrorMessage(ev.text);
      break;
  }
}

async function reloadRuns(): Promise<void> {
  try {
    app.runs = await api.run.list();
  } catch (err) {
    app.error = errorMessage(err);
  }
}

async function reloadPrds(): Promise<void> {
  try {
    app.prds = await api.prd.list();
    if (app.selectedPrd) {
      app.detail = await api.prd.get(app.selectedPrd);
      // The first commit of a PRD is what makes publishing possible, and that
      // happens mid-run — so the offer is re-asked whenever the PRD changes
      // rather than only on selection.
      void reloadPublishOffer(app.selectedPrd);
    }
  } catch {
    // A PRD being rewritten underneath us is normal — the agent edits progress
    // notes constantly. The next event will bring us back into sync.
  }
}

/**
 * Open and focus the New PRD tab. The desktop File ▸ New PRD menu item and its
 * ⌘N / Ctrl+N shortcut both route here, as does the in-window "New PRD" tab.
 *
 * It only reveals the tab — the authoring session is started from inside the
 * pane, on an explicit action — so handling the same command more than once is
 * a no-op rather than a second session. It is ignored while a modal question is
 * awaiting an answer, so the shortcut cannot pull focus off a prompt that is
 * blocking its run.
 */
/**
 * Open or close the settings dialog.
 *
 * Ignored while a question is waiting, for the same reason New PRD is: a native
 * accelerator fires regardless of what the webview is showing, and stacking a
 * dialog over a decision that blocks a run helps nobody.
 */
export function toggleSettings(): void {
  if (app.questions.length > 0) return;
  app.settingsOpen = !app.settingsOpen;
}

export function closeSettings(): void {
  app.settingsOpen = false;
}

export function requestNewPRD(): void {
  if (app.questions.length > 0) return;
  app.newPrdOpen = true;
}

export function closeNewPRD(): void {
  app.newPrdOpen = false;
}

/**
 * Create a PRD and hand it straight to an authoring conversation.
 *
 * The PRD exists on disk before the agent is asked for anything, so it is in
 * the sidebar immediately and survives a conversation that is abandoned. The
 * caller then starts the session against a PRD that is already real.
 */
export async function createPrd(req: NewPRDRequest): Promise<string | null> {
  try {
    const created = await api.prd.create(req);
    app.newPrdOpen = false;
    await reloadPrds();
    await selectPrd(created.name);
    app.authorTarget = { kind: "new", prd: created.name };
    app.view = "author";
    return created.name;
  } catch (err) {
    app.error = errorMessage(err);
    return null;
  }
}

/**
 * Open a PRD for conversational editing.
 *
 * The PRD is read first: a missing or unreadable one must produce an actionable
 * error rather than opening an authoring tab that would go on to create an empty
 * replacement. Routed here from the rail's action menus, so the target is always
 * the row the menu was opened on, not whatever happens to be selected.
 */
export async function editPrd(name: string): Promise<void> {
  try {
    await api.prd.get(name);
  } catch (err) {
    app.error = `${name} cannot be opened for editing. ${errorMessage(err)}`;
    return;
  }
  await selectPrd(name);
  app.authorTarget = { kind: "edit", prd: name };
  app.view = "author";
}

/** Open a PRD's prd.md in the user's default editor. */
export async function openPrdFile(name: string): Promise<void> {
  try {
    await api.prd.openFile(name);
  } catch (err) {
    app.error = errorMessage(err);
  }
}

/**
 * Ask before deleting. Deletion removes a directory of work, so the action menu
 * raises a dialog naming its target rather than acting on the click.
 */
export function confirmDeletePrd(name: string): void {
  app.pendingDelete = name;
}

/** Dismiss the confirmation, leaving the PRD and its files untouched. */
export function cancelDeletePrd(): void {
  app.pendingDelete = null;
}

/**
 * Delete a PRD and everything under it. Clears the selection when the deleted
 * PRD was the selected one, so the rail does not point at a row that is gone.
 *
 * The rail entry is removed only after the backend confirms: a refused delete —
 * the PRD is being implemented, or has an authoring session open — must leave
 * the list exactly as it was, with the reason on screen.
 */
export async function deletePrd(name: string): Promise<void> {
  app.pendingDelete = null;
  try {
    await api.prd.delete(name);
    if (app.selectedPrd === name) {
      app.selectedPrd = null;
      app.detail = null;
    }
    await reloadPrds();
  } catch (err) {
    app.error = errorMessage(err);
    // Re-read the authoritative list so a partially applied or refused delete
    // cannot leave the rail disagreeing with what is on disk.
    await reloadPrds();
  }
}

/**
 * Open the project's GitHub repository in the default browser.
 *
 * Resolving the remote is the Go side's job; a project with no GitHub remote
 * comes back as an error and opens nothing rather than falling back to a URL
 * that would point somewhere unrelated.
 */
export async function openOnGitHub(): Promise<void> {
  if (app.launching) return;
  app.launching = true;
  try {
    await api.project.openOnGitHub();
  } catch (err) {
    app.error = errorMessage(err);
  } finally {
    app.launching = false;
  }
}

/**
 * Open the repository root in a local editor.
 *
 * The guard is per activation rather than per app: overlapping UI and native
 * events would otherwise launch the editor twice from one click.
 */
export async function openInApp(target: string): Promise<void> {
  if (app.launching) return;
  app.launching = true;
  try {
    await api.project.openInApp(target);
  } catch (err) {
    app.error = errorMessage(err);
  } finally {
    app.launching = false;
  }
}

/**
 * Load the resolved per-phase agent defaults.
 *
 * The defaults are resolved on the Go side so the selectors can show the actual
 * agent rather than a blank that ambiguously means "whatever is configured".
 */
export async function reloadCreationOptions(): Promise<void> {
  try {
    app.agentDefaults = await api.project.agentDefaults();
  } catch {
    app.agentDefaults = null;
  }
}

/**
 * Save a PRD's workflow settings.
 *
 * This configures the later implementation run and nothing else: no branch, no
 * pull request and no tracker write happens here.
 */
export async function savePrdWorkflow(name: string, workflow: PRDWorkflow): Promise<boolean> {
  try {
    await api.prd.saveWorkflow(name, workflow);
    return true;
  } catch (err) {
    app.error = errorMessage(err);
    return false;
  }
}

/** Load which editors are installed, for the AI IDE dropdown's secondary text. */
export async function reloadLocalApps(): Promise<void> {
  try {
    app.localApps = await api.project.localApps();
  } catch {
    // Detection is advisory: a failure leaves the entries selectable, and
    // activating one still produces the real, actionable error.
    app.localApps = [];
  }
}

export async function selectPrd(name: string): Promise<void> {
  app.selectedPrd = name;
  app.selectedStory = null;
  // Cleared rather than left standing: the previous PRD's answer would offer the
  // control for a PRD that may have nothing to publish at all.
  app.publishOffer = null;
  app.published = null;
  app.publishedStack = null;
  try {
    app.detail = await api.prd.get(name);
    const first = app.detail.stories?.find((s) => s.status !== "done");
    app.selectedStory = first?.id ?? app.detail.stories?.[0]?.id ?? null;
  } catch (err) {
    app.error = errorMessage(err);
  }
  void refreshPullRequests(name);
  void reloadPublishOffer(name);
}

/**
 * Re-read pull request state from GitHub for a PRD, in the background.
 *
 * Deliberately not awaited by its callers. It shells out to gh, which takes the
 * better part of a second, and the cached state is already on screen by then —
 * blocking selection on a network round trip to sharpen something already
 * rendered would be the wrong trade every time.
 *
 * Failure is silent by design. A project with no GitHub remote, no gh, or no
 * network is a normal project; the branch names are still right and the reason
 * is carried in the result rather than raised as an error.
 */
export async function refreshPullRequests(name: string): Promise<void> {
  try {
    const set = await api.prd.refreshPullRequests(name);
    // Nothing was confirmed, so nothing on screen should change.
    if (!set?.checkedAt) return;
    // Re-read the detail rather than patching it: the Go side has already
    // written the fresh refs into the sidecar, and one path that assembles a
    // PRD is easier to trust than two.
    if (app.selectedPrd === name) app.detail = await api.prd.get(name);
  } catch {
    // See above: an unreachable GitHub is not an error worth showing.
  }
}

/**
 * Ask the Go side whether this PRD can be published.
 *
 * Not derivable here: it depends on git mode and on whether any story has
 * committed, and the engine is the only thing that knows either. A failure
 * leaves no offer, which hides the control — the safe direction, since the
 * alternative is a button whose press can only produce an error.
 */
export async function reloadPublishOffer(name: string): Promise<void> {
  try {
    const offer = await api.prd.publishOffer(name);
    if (app.selectedPrd === name) app.publishOffer = offer;
  } catch {
    if (app.selectedPrd === name) app.publishOffer = null;
  }
}

/**
 * Push the PRD's branch and open — or update — its pull request.
 *
 * The refusals live in the engine, so a failure here is reported verbatim
 * rather than second-guessed: "the run is still going" is the reason, and
 * rewording it would only make it less true.
 */
export async function publishPullRequest(draft: boolean): Promise<void> {
  const prd = app.selectedPrd;
  if (!prd || app.publishing) return;

  app.publishing = true;
  app.error = null;
  try {
    app.published = await api.prd.publish({ prd, draft } as never);
    // The pull request is now cached against the branch, so the links the story
    // list and the header show come back with it.
    await reloadPrds();
  } catch (err) {
    app.error = errorMessage(err);
  } finally {
    app.publishing = false;
  }
}

/**
 * Open one pull request per story, from the bottom of the stack upwards.
 *
 * A stack that stops half way still created what it created, and says so twice:
 * `failed` becomes the error banner, and the per-story list underneath the control
 * says which stories have a pull request and which do not. Pressing again is the
 * retry — it attempts only what is missing.
 *
 * The PRD is reloaded either way, including after a rejected call: each pull
 * request was recorded against its branch as it was opened, which is what puts the
 * links back on the story rows.
 */
export async function publishStack(draft: boolean): Promise<void> {
  const prd = app.selectedPrd;
  if (!prd || app.publishing) return;

  app.publishing = true;
  app.error = null;
  app.publishedStack = null;
  try {
    const report = await api.prd.publishStack({ prd, draft } as never);
    app.publishedStack = report;
    app.error = report.failed || null;
  } catch (err) {
    app.error = errorMessage(err);
  } finally {
    app.publishing = false;
    await reloadPrds();
  }
}

/** Show the native folder chooser. A dismissed dialog is not an error. */
export async function pickProject(): Promise<void> {
  app.error = null;
  try {
    const project = await api.project.pick();
    if (!project) return;
    app.selectedPrd = null;
    await refresh();
  } catch (err) {
    app.error = errorMessage(err);
  }
}

export async function openProject(path: string): Promise<void> {
  app.error = null;
  try {
    await api.project.open(path);
    app.selectedPrd = null;
    await refresh();
  } catch (err) {
    app.error = errorMessage(err);
  }
}

// ------------------------------------------------------------------- runs --

/**
 * Start implementing the selected PRD.
 *
 * `agent` overrides the PRD's saved implementation agent for this run only; the
 * confirmation step passes it when the user changes their mind at the last
 * moment. Without one, the Go side resolves the PRD's own choice and refuses if
 * that agent has since been uninstalled rather than substituting another.
 */
export async function startRun(agent?: string): Promise<void> {
  const prd = app.selectedPrd;
  if (!prd) return;
  // At most one session per PRD: refuse while a Start (or any action) for this
  // PRD is still resolving, or while a live session already exists.
  if (app.pending[prd]) return;
  const existing = app.runs.find((r) => r.prd === prd);
  if (existing && isActive(existing.state)) return;

  setPending(prd, "starting");
  // Dismiss any stale start-error dialog for this PRD; a fresh failure re-sets it.
  app.error = null;
  try {
    await api.run.start({ prd, provider: agent || undefined } as never);
    await reloadRuns();
  } catch (err) {
    app.error = errorMessage(err);
  } finally {
    clearPending(prd);
  }
}

export function pauseRun(): Promise<void> {
  return transition("pausing", (s) => s === LoopState.StateRunning, (id) => api.run.pause(id));
}

export function resumeRun(): Promise<void> {
  return transition("resuming", (s) => s === LoopState.StatePaused, (id) => api.run.resume(id));
}

export function stopRun(): Promise<void> {
  return transition("stopping", isActive, (id) => api.run.stop(id));
}

/**
 * Fire a Pause/Resume/Stop and track it as an in-flight transition.
 *
 * The action only fires when the authoritative state allows it and no other
 * request for the PRD is outstanding, so duplicate requests are impossible.
 * On failure the run list is re-fetched to reconcile with whatever the backend
 * actually did before the error is surfaced, leaving the controls valid for the
 * reconciled state.
 */
async function transition(
  kind: Transition,
  allowed: (state: LoopState) => boolean,
  fn: (id: string) => Promise<unknown>,
): Promise<void> {
  const prd = app.selectedPrd;
  const run = app.currentRun;
  if (!prd || !run) return;
  if (app.pending[prd]) return;
  if (!allowed(run.state)) return;

  setPending(prd, kind);
  app.error = null;
  try {
    await fn(run.id);
    await reloadRuns();
  } catch (err) {
    await reloadRuns();
    app.error = errorMessage(err);
  } finally {
    clearPending(prd);
  }
}

function setPending(prd: string, kind: Transition): void {
  app.pending = { ...app.pending, [prd]: kind };
}

function clearPending(prd: string): void {
  const next = { ...app.pending };
  delete next[prd];
  app.pending = next;
}

export async function adjustBudget(delta: number): Promise<void> {
  const run = app.currentRun;
  if (!run) return;
  await guard(() => api.run.setAttemptBudget(run.id, run.attemptBudget + delta));
}

/**
 * Resolves a question. The fields ride with the option because they qualify it:
 * the branch layout and the branch name are part of the same decision as "where
 * should this run commit", not a second prompt after it.
 */
export async function answerQuestion(
  id: string,
  optionId: string,
  inputs: Record<string, string> = {},
): Promise<void> {
  await guard(() => api.run.answer(id, { optionId, inputs } as never));
}

async function guard(fn: () => Promise<unknown>): Promise<void> {
  try {
    await fn();
    await reloadRuns();
  } catch (err) {
    app.error = errorMessage(err);
  }
}

// -------------------------------------------------------------- utilities --

/** States a PRD can be started from: no live session is in progress. */
const STARTABLE = new Set<LoopState>([
  LoopState.StateIdle,
  LoopState.StateStopped,
  LoopState.StateComplete,
  LoopState.StateError,
]);

/** No session, or one in a terminal/never-started state, can be (re)started. */
function isStartable(state: LoopState | undefined): boolean {
  return state === undefined || STARTABLE.has(state);
}

/** A session that Pause/Resume/Stop can act on. Awaiting is running-but-blocked
 *  on a question, so it is still stoppable. */
function isActive(state: LoopState | undefined): boolean {
  return (
    state === LoopState.StateRunning ||
    state === LoopState.StatePaused ||
    state === LoopState.StateAwaiting
  );
}

function truncate(s: string, n: number): string {
  const flat = s.replace(/\s+/g, " ").trim();
  return flat.length > n ? flat.slice(0, n - 1) + "…" : flat;
}

/** The headline argument of a tool call, for the activity ticker. */
export function toolArgument(ev: LoopEvent): string {
  const input = ev.agent?.toolInput as Record<string, unknown> | undefined;
  if (!input) return "";
  for (const key of ["file_path", "path", "command", "pattern", "url", "description"]) {
    const v = input[key];
    if (typeof v === "string" && v) return truncate(v, 80);
  }
  return "";
}
