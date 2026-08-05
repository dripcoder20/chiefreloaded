<script lang="ts">
  import { app, savePrdWorkflow } from "../stores/app.svelte";
  import { api, type PRDWorkflow } from "../platform";

  /**
   * The selected PRD's own workflow: which agent implements it, whether it
   * stacks a pull request per story, and where its stories are published.
   *
   * These were only settable while creating a PRD, which meant a decision made
   * once could never be revisited — and the project-wide Settings dialog was
   * the wrong home for them, since they belong to one document rather than to
   * the project.
   */

  let workflow = $state<PRDWorkflow | null>(null);
  let loadedFor = $state<string | null>(null);
  let saving = $state(false);

  const prd = $derived(app.selectedPrd);

  const installedAgents = $derived(
    (app.environment?.agents ?? []).filter((a) => a.available).map((a) => a.name),
  );

  // Reload whenever the selection changes, so the panel is never showing one
  // PRD's settings under another's name.
  $effect(() => {
    const name = prd;
    if (!name || loadedFor === name) return;
    loadedFor = name;
    void load(name);
  });

  async function load(name: string): Promise<void> {
    workflow = null;
    const got = await api.prd.workflow(name).catch(() => null);
    // Only adopt it if the selection has not moved on while we were reading.
    if (loadedFor === name) {
      workflow = got ?? ({ implementationAgent: "", stackPerStory: false, issueDestination: "" } as PRDWorkflow);
    }
  }

  /**
   * Saves on change rather than behind a button. There is no partially valid
   * state here — every control is a complete choice on its own — so a save step
   * would only be a way to lose one.
   */
  async function update(change: (w: PRDWorkflow) => void): Promise<void> {
    if (!workflow || !prd) return;
    const next = { ...workflow };
    change(next);
    workflow = next;

    saving = true;
    try {
      await savePrdWorkflow(prd, next);
    } finally {
      saving = false;
    }
  }
</script>

<div class="prd-settings">
  <div class="content">
    {#if !prd}
      <p class="muted">No PRD selected.</p>
    {:else if !workflow}
      <p class="muted">Loading {prd}…</p>
    {:else}
      <section>
        <h3>Implementation</h3>

        <label>
          <span>Agent</span>
          <select
            value={workflow.implementationAgent ?? ""}
            aria-label="Implementation agent"
            onchange={(e) => update((w) => (w.implementationAgent = e.currentTarget.value))}
          >
            <option value="">Project default</option>
            {#each installedAgents as agent (agent)}
              <option value={agent}>{agent}</option>
            {/each}
          </select>
        </label>
        <p class="help">
          Used when this PRD is implemented. Project default follows Settings; you can
          still change it at Start.
        </p>

        <label class="check">
          <input
            type="checkbox"
            checked={workflow.stackPerStory ?? false}
            onchange={(e) => update((w) => (w.stackPerStory = e.currentTarget.checked))}
          />
          <span class="check-label">Stack a pull request per user story</span>
        </label>
        <p class="help">
          Overrides the project's git mode for this PRD alone. Applied when the run
          starts — nothing is branched or opened now.
        </p>
      </section>

      <section>
        <h3>Issues</h3>

        <label>
          <span>Publish to</span>
          <select
            value={workflow.issueDestination ?? ""}
            aria-label="Publish issues to"
            onchange={(e) => update((w) => (w.issueDestination = e.currentTarget.value as never))}
          >
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
      </section>

      <p class="status" aria-live="polite">{saving ? "Saving…" : "Saved automatically"}</p>
    {/if}
  </div>
</div>

<style>
  /* Full width so its scrollbar sits at the pane edge; the readable cap belongs
     to the content rather than to the box that scrolls. */
  .prd-settings {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
  }

  .content {
    padding: 16px 18px;
    max-width: 640px;
  }

  section {
    margin-bottom: 22px;
  }

  h3 {
    margin: 0 0 10px;
    font-size: 12px;
    font-weight: 500;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--fg-3);
  }

  label {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 8px;
  }
  label span {
    width: 150px;
    flex: none;
    color: var(--fg-2);
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

  .help {
    margin: -4px 0 10px 162px;
    font-size: 11.5px;
    color: var(--fg-3);
    max-width: 52ch;
  }

  .muted,
  .status {
    color: var(--fg-3);
  }
  .status {
    font-size: 11.5px;
  }
</style>
