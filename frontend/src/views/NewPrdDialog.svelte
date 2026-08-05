<script lang="ts">
  import { onMount, tick } from "svelte";
  import { app, createPrd } from "../stores/app.svelte";

  /**
   * Everything needed to start a PRD, asked once.
   *
   * These choices used to live in a tab that competed with the stories of
   * whatever PRD was selected — a screen about a thing that did not exist yet,
   * sitting beside screens about one that did. As a dialog it is a single
   * decision with an obvious end, and the tab strip goes back to being about
   * the selected PRD only.
   *
   * Pressing Create writes the PRD before the agent is asked for anything, so
   * it appears in the sidebar immediately and survives a conversation that is
   * abandoned.
   */
  let { onclose }: { onclose: () => void } = $props();

  let name = $state("");
  let context = $state("");
  let authoringAgent = $state("");
  let implementationAgent = $state("");
  let stackPerStory = $state(false);
  let issueDestination = $state("");
  let creating = $state(false);

  let dialogEl = $state<HTMLDivElement | null>(null);
  let nameEl = $state<HTMLInputElement | null>(null);
  let opener: HTMLElement | null = null;

  const nameValid = $derived(/^[A-Za-z0-9_-]+$/.test(name));
  const taken = $derived(app.prds.some((p) => p.name === name));

  /**
   * Only installed agents are offered. Listing one that is not on the machine
   * turns a wrong choice into a failure at start time rather than at choice
   * time, which is the harder failure to understand.
   */
  const installedAgents = $derived(
    (app.environment?.agents ?? []).filter((a) => a.available).map((a) => a.name),
  );

  // Adopt the resolved defaults once they arrive, without overwriting a choice
  // already made.
  $effect(() => {
    const defaults = app.agentDefaults;
    if (!defaults) return;
    if (!authoringAgent) authoringAgent = defaults.authoring;
    if (!implementationAgent) implementationAgent = defaults.implementation;
  });

  onMount(() => {
    opener = document.activeElement as HTMLElement | null;
    void focusName();
    return () => opener?.focus?.();
  });

  async function focusName(): Promise<void> {
    await tick();
    nameEl?.focus();
  }

  async function create(): Promise<void> {
    if (!nameValid || taken || creating) return;
    creating = true;
    try {
      await createPrd({
        name,
        context,
        workflow: { implementationAgent, stackPerStory, issueDestination },
      } as never);
    } finally {
      creating = false;
    }
  }

  const FOCUSABLE =
    'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === "Escape") {
      event.stopPropagation();
      onclose();
      return;
    }
    if (event.key !== "Tab" || !dialogEl) return;

    const focusables = [...dialogEl.querySelectorAll<HTMLElement>(FOCUSABLE)];
    if (focusables.length === 0) return;
    const first = focusables[0];
    const last = focusables[focusables.length - 1];

    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
      return;
    }
    if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }
</script>

<!-- Decorative: Escape and Cancel are the keyboard paths. -->
<div
  class="overlay"
  role="presentation"
  onclick={(e) => {
    if (e.target === e.currentTarget) onclose();
  }}
>
  <div
    class="panel"
    role="dialog"
    aria-modal="true"
    aria-label="New PRD"
    tabindex="-1"
    bind:this={dialogEl}
    onkeydown={onKeydown}
  >
    <header class="head">
      <h2>New PRD</h2>
    </header>

    <div class="body">
      <label>
        <span>Name</span>
        <input
          type="text"
          bind:value={name}
          bind:this={nameEl}
          placeholder="secondary-email"
          spellcheck="false"
          onkeydown={(e) => e.key === "Enter" && create()}
        />
      </label>
      {#if name && !nameValid}
        <p class="err">
          Letters, digits, hyphen and underscore only — it becomes a folder and a branch
          name.
        </p>
      {:else if taken}
        <p class="err">{name} already exists. Pick another name.</p>
      {/if}

      <label class="tall">
        <span>What are you building?</span>
        <textarea
          bind:value={context}
          rows="4"
          placeholder="Optional. Leave blank and the agent will ask."
        ></textarea>
      </label>
      <p class="help">Saved with the PRD, so it survives a conversation you abandon.</p>

      <h3>Agents</h3>

      <label>
        <span>Authoring agent</span>
        <select bind:value={authoringAgent} aria-label="Authoring agent">
          {#each installedAgents as agent (agent)}
            <option value={agent}>{agent}</option>
          {/each}
        </select>
      </label>
      <p class="help">Writes this PRD with you, now.</p>

      <label>
        <span>Implementation agent</span>
        <select bind:value={implementationAgent} aria-label="Implementation agent">
          {#each installedAgents as agent (agent)}
            <option value={agent}>{agent}</option>
          {/each}
        </select>
      </label>
      <p class="help">Executes the stories later. You can still change it at Start.</p>

      <h3>Implementation workflow</h3>

      <label class="check">
        <input type="checkbox" bind:checked={stackPerStory} />
        <span class="check-label">Stack a pull request per user story</span>
      </label>
      <p class="help">
        Applied when implementation starts. Choosing it creates no branches or pull
        requests now.
      </p>

      <label>
        <span>Publish issues to</span>
        <select bind:value={issueDestination} aria-label="Publish issues to">
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
    </div>

    <footer class="foot">
      <button onclick={onclose}>Cancel</button>
      <button class="primary" disabled={!nameValid || taken || creating} onclick={create}>
        {creating ? "Creating…" : "Create PRD"}
      </button>
    </footer>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 60;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 32px;
    background: rgba(0, 0, 0, 0.45);
  }

  .panel {
    display: flex;
    flex-direction: column;
    width: min(680px, 100%);
    max-height: min(720px, 100%);
    min-height: 0;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--radius-panel, 8px);
    box-shadow: 0 24px 64px rgba(0, 0, 0, 0.45);
    overflow: hidden;
  }

  .head,
  .foot {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px 16px;
    flex: none;
  }
  .head {
    border-bottom: 1px solid var(--border);
  }
  .foot {
    border-top: 1px solid var(--border);
    justify-content: flex-end;
  }

  h2 {
    margin: 0;
    font-size: 14px;
    font-weight: 500;
    color: var(--fg-1);
  }

  h3 {
    margin: 18px 0 8px;
    font-size: 12px;
    font-weight: 500;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--fg-3);
  }

  /* The width cap is on the content, not on the scrolling box, so the scrollbar
     sits at the panel edge rather than floating inside it. */
  .body {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 16px 18px;
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
  textarea,
  select {
    flex: 1;
    padding: 5px 8px;
    background: var(--bg-raised);
    color: var(--fg-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    font: inherit;
    resize: vertical;
  }
  input[type="text"] {
    font-family: var(--font-mono);
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

  .err {
    margin: -4px 0 10px 162px;
    font-size: 12px;
    color: var(--danger);
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
</style>
