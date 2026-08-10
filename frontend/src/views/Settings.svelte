<script lang="ts">
  import { api, type Settings } from "../platform";
  import { app } from "../stores/app.svelte";
  import { celebration } from "../stores/celebration.svelte";
  import { errorMessage } from "../stores/errors";
  import PromptEditor from "./PromptEditor.svelte";

  let promptKind = $state<"new" | "edit">("new");

  /**
   * chief's settings screen exposes three fields and hides the agent provider
   * entirely, so choosing between Claude and Codex means hand-editing YAML. It
   * also has nowhere to put Loop's git mode — which is the whole reason this
   * application exists, and is useless if it cannot be switched on.
   */

  let saving = $state(false);
  let saved = $state(false);

  const cfg = $derived(app.config);
  const env = $derived(app.project);

  async function update(mutate: (c: Settings) => void): Promise<void> {
    if (!app.config) return;
    // Clone: mutating the store object in place makes a failed save impossible
    // to roll back, and the UI would show a setting that is not on disk.
    const next = structuredClone($state.snapshot(app.config)) as Settings;
    mutate(next);

    saving = true;
    try {
      await api.project.saveConfig(next);
      app.config = next;
      saved = true;
      setTimeout(() => (saved = false), 1500);
    } catch (err) {
      app.error = errorMessage(err);
    } finally {
      saving = false;
    }
  }

  // An empty cost field means "no warning" (nil on the Go side); any number is
  // taken verbatim so the config layer can validate it and reject a bad value.
  function costFromInput(raw: string): number | undefined {
    const trimmed = raw.trim();
    if (trimmed === "") return undefined;
    return Number(trimmed);
  }
</script>

<div class="settings">
  <div class="content">
  {#if !cfg}
    <p class="muted">No project open.</p>
  {:else}
    <section>
      <h3>Git</h3>

      <label class="row">
        <span class="label">Mode</span>
        <select
          value={cfg.git.mode}
          onchange={(e) => update((c) => (c.git.mode = e.currentTarget.value as never))}
        >
          <option value="off">off — the agent commits, nothing else</option>
          <option value="per-prd">per-prd — one branch for the whole PRD</option>
          <option value="per-story">per-story — a stacked branch and draft PR per story</option>
        </select>
      </label>

      {#if cfg.git.mode === "per-story"}
        <p class="note">
          Every story gets its own branch stacked on the one below it, pushed with a draft
          pull request the moment it completes. The loop does not wait for review.
        </p>

        <label class="row">
          <span class="label">Draft pull requests</span>
          <input
            type="checkbox"
            checked={cfg.git.draft}
            onchange={(e) => update((c) => (c.git.draft = e.currentTarget.checked))}
          />
        </label>

        <label class="row">
          <span class="label">Require a worktree</span>
          <input
            type="checkbox"
            checked={cfg.git.requireWorktree}
            onchange={(e) => update((c) => (c.git.requireWorktree = e.currentTarget.checked))}
          />
        </label>
        {#if !cfg.git.requireWorktree}
          <p class="warn">
            Without a worktree the loop switches branches in your own checkout between
            stories, moving your working tree while you are using it.
          </p>
        {/if}

        <label class="row">
          <span class="label">Branch template</span>
          <input
            type="text"
            value={cfg.git.branchTemplate}
            onchange={(e) => update((c) => (c.git.branchTemplate = e.currentTarget.value))}
          />
        </label>

        <label class="row">
          <span class="label">Base branch</span>
          <input
            type="text"
            placeholder={app.project?.defaultBase || "the repository default"}
            value={cfg.git.baseBranch}
            onchange={(e) => update((c) => (c.git.baseBranch = e.currentTarget.value))}
          />
        </label>
      {/if}
    </section>

    <section>
      <h3>Agent</h3>
      <label class="row">
        <span class="label">Provider</span>
        <select
          value={cfg.agent.provider || "claude"}
          onchange={(e) => update((c) => (c.agent.provider = e.currentTarget.value))}
        >
          <option value="claude">Claude Code</option>
          <option value="codex">Codex</option>
          <option value="opencode">OpenCode</option>
          <option value="cursor">Cursor</option>
        </select>
      </label>
      <label class="row">
        <span class="label">CLI path</span>
        <input
          type="text"
          placeholder="found on PATH"
          value={cfg.agent.cliPath}
          onchange={(e) => update((c) => (c.agent.cliPath = e.currentTarget.value))}
        />
      </label>

      <p class="warn">
        Agents run with permission checks disabled. Anything Loop drives can run commands
        and edit files in the project directory without asking.
      </p>
    </section>

    <section>
      <h3>Usage warnings</h3>
      <p class="lead">
        Highlight an agent approaching a context limit or exceeding an expected cost. A
        warning is informational only — it never pauses or stops a run.
      </p>

      <label class="row">
        <span class="label">Context warning %</span>
        <input
          type="number"
          min="1"
          max="99"
          value={cfg.usage?.contextWarnPercent ?? 80}
          onchange={(e) =>
            update((c) => (c.usage.contextWarnPercent = Number(e.currentTarget.value)))}
        />
      </label>

      <label class="row">
        <span class="label">Context critical %</span>
        <input
          type="number"
          min="1"
          max="100"
          value={cfg.usage?.contextCriticalPercent ?? 95}
          onchange={(e) =>
            update((c) => (c.usage.contextCriticalPercent = Number(e.currentTarget.value)))}
        />
      </label>

      <label class="row">
        <span class="label">Session cost warning</span>
        <input
          type="number"
          min="0"
          step="0.01"
          placeholder="no warning"
          value={cfg.usage?.costWarnAmount ?? ""}
          onchange={(e) =>
            update((c) => (c.usage.costWarnAmount = costFromInput(e.currentTarget.value)))}
        />
      </label>

      <p class="note">
        The critical percent must be greater than the warning percent. Invalid values are
        rejected and not saved.
      </p>
    </section>

    <section class="wide">
      <h3>PRD prompt</h3>
      <p class="lead">
        The brief the agent receives when writing a PRD. Put your own conventions or
        slash commands here and they apply to every PRD you create.
      </p>
      <div class="kinds">
        <button class:on={promptKind === "new"} onclick={() => (promptKind = "new")}>
          Creating
        </button>
        <button class:on={promptKind === "edit"} onclick={() => (promptKind = "edit")}>
          Editing
        </button>
      </div>
      <PromptEditor kind={promptKind} />
    </section>

    <section>
      <h3>Worktree</h3>
      <label class="row">
        <span class="label">Setup command</span>
        <input
          type="text"
          placeholder="npm ci"
          value={cfg.worktree.setup}
          onchange={(e) => update((c) => (c.worktree.setup = e.currentTarget.value))}
        />
      </label>
      <p class="note">Run once after a worktree is created. Keep it idempotent.</p>
    </section>

    <p class="status" class:on={saving || saved}>
      {saving ? "Saving…" : saved ? "Saved to .chief/config.yaml" : ""}
    </p>
    {#if env}
      <p class="muted">
        Shared with the chief TUI — it ignores the keys it does not recognise, so the same
        project still opens in both.
      </p>
    {/if}
  {/if}

    <!-- Outside the project gate on purpose: this is a preference of the person
         at the screen, not of the project, so it is answerable with no project
         open and it never reaches saveConfig. -->
    <section>
      <h3>Celebration</h3>
      <label class="row">
        <span class="label">Confetti on publish</span>
        <input type="checkbox" bind:checked={celebration.isCelebrationEnabled} />
      </label>
      <p class="note">
        A short burst when a pull request or a whole stack finishes publishing. Remembered
        in this browser only — it is not written to .chief/config.yaml, so turning it off
        does not turn it off for anyone else working on the project.
      </p>
    </section>
  </div>
</div>

<style>
  /* The scroll container is full width so its scrollbar sits at the edge of the
     pane rather than floating in the middle of it. The readable width cap
     belongs to the content, not to the thing that scrolls. */
  .settings {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
  }

  .content {
    padding: 16px 18px;
    max-width: 640px;
  }

  /* The prompt editor needs room to read as a document rather than a field.
     No negative margin: with the cap off the scroll container there is nothing
     to bleed past, and the bleed was what produced a horizontal scrollbar. */
  section.wide {
    max-width: none;
  }

  .lead {
    margin: -4px 0 10px;
    font-size: 12px;
    color: var(--fg-3);
    max-width: 68ch;
  }

  .kinds {
    display: flex;
    gap: 4px;
    margin-bottom: 10px;
  }
  .kinds button {
    padding: 3px 10px;
    background: none;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    color: var(--fg-3);
    font: inherit;
    cursor: default;
  }
  .kinds button.on {
    color: var(--fg-1);
    border-color: var(--accent);
  }

  section {
    margin-bottom: 24px;
  }

  h3 {
    margin: 0 0 10px;
    font-size: 11px;
    font-weight: 500;
    letter-spacing: 0.06em;
    color: var(--fg-3);
    text-transform: uppercase;
  }

  .row {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 8px;
  }

  .label {
    width: 150px;
    flex: none;
    color: var(--fg-2);
  }

  select,
  input[type="text"],
  input[type="number"] {
    flex: 1;
    padding: 4px 8px;
    background: var(--bg-raised);
    color: var(--fg-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    font: inherit;
  }

  input[type="text"],
  input[type="number"] {
    font-family: var(--font-mono);
    font-size: 12px;
  }

  .note,
  .warn,
  .muted {
    margin: 4px 0 12px 162px;
    font-size: 12px;
    color: var(--fg-3);
  }

  .warn {
    color: var(--warn);
    border-left: 2px solid var(--warn);
    padding-left: 8px;
  }

  .status {
    height: 16px;
    margin: 0 0 4px 162px;
    font-size: 12px;
    color: var(--ok);
    opacity: 0;
    transition: opacity var(--dur-hover) var(--ease);
  }
  .status.on {
    opacity: 1;
  }

  .muted {
    margin-left: 162px;
  }
</style>
