<script lang="ts">
  import { app, selectPrd } from "../stores/app.svelte";

  /**
   * The PRD list is navigation, not a dialog.
   *
   * chief hides it behind a modal you open with `l`, which occludes everything —
   * yet it shows live per-PRD state with a ticking iteration counter, i.e. it is
   * something you are meant to watch. Those two facts contradict each other in a
   * terminal and do not have to here, so it becomes a permanent rail. It also
   * absorbs chief's tab bar, which renders the same list a second time.
   */

  function stateOf(name: string): string {
    return app.runs.find((r) => r.prd === name)?.state ?? "idle";
  }
</script>

<nav class="rail" aria-label="PRDs">
  <div class="head">PRDS</div>

  {#if app.prds.length === 0}
    <p class="empty">No PRDs found.</p>
  {/if}

  {#each app.prds as prd (prd.name)}
    {@const state = stateOf(prd.name)}
    <button
      class="row"
      class:current={app.selectedPrd === prd.name}
      onclick={() => selectPrd(prd.name)}
    >
      <span class="line">
        <span class="dot {state}" class:pulse={state === "running"}></span>
        <span class="name">{prd.name}</span>
        <span class="count tnum">{prd.completed}/{prd.total}</span>
      </span>

      <span class="meter" aria-hidden="true">
        <span
          class="fill {state}"
          style="width: {prd.total ? (prd.completed / prd.total) * 100 : 0}%"
        ></span>
      </span>

      {#if prd.parseError}
        <span class="warn">unreadable</span>
      {:else if prd.legacy}
        <span class="warn">legacy layout</span>
      {:else if prd.branch}
        <span class="branch">⎇ {prd.branch}</span>
      {/if}
    </button>
  {/each}
</nav>

<style>
  .rail {
    display: flex;
    flex-direction: column;
    width: 236px;
    flex: none;
    border-right: 1px solid var(--border);
    background: var(--bg-panel);
    overflow-y: auto;
  }

  .head {
    padding: 12px 14px 6px;
    font-size: 11px;
    font-weight: 500;
    letter-spacing: 0.06em;
    color: var(--fg-3);
  }

  .empty {
    padding: 4px 14px;
    color: var(--fg-3);
    margin: 0;
  }

  .row {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 7px 14px;
    background: none;
    border: 0;
    border-left: 2px solid transparent;
    color: var(--fg-2);
    font: inherit;
    text-align: left;
    cursor: default;
  }

  .row:hover {
    background: var(--bg-raised);
  }
  .row.current {
    background: var(--bg-raised);
    border-left-color: var(--accent);
    color: var(--fg-1);
  }

  .line {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .name {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .count {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
  }

  /* A filled dot for idle, a pulsing one for running: motion means work is
     happening, and it is the peripheral-vision signal that something is live. */
  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--fg-3);
    flex: none;
  }
  .dot.running {
    background: var(--accent);
  }
  .dot.complete {
    background: var(--ok);
  }
  .dot.paused {
    background: var(--warn);
  }
  .dot.error {
    background: var(--danger);
  }

  .pulse {
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

  .meter {
    display: block;
    height: 3px;
    background: var(--n-3);
    border-radius: 2px;
    overflow: hidden;
  }

  .fill {
    display: block;
    height: 100%;
    background: var(--fg-3);
  }
  .fill.running {
    background: var(--accent);
  }
  .fill.complete {
    background: var(--ok);
  }

  .branch,
  .warn {
    font-family: var(--font-mono);
    font-size: 10.5px;
    color: var(--fg-3);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .warn {
    color: var(--warn);
  }

  @media (prefers-reduced-motion: reduce) {
    /* Keep the distinction, drop the animation. */
    .pulse {
      animation: none;
      opacity: 1;
    }
  }
</style>
