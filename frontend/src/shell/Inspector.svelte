<script lang="ts">
  import { app } from "../stores/app.svelte";

  const story = $derived(app.detail?.stories?.find((s) => s.id === app.selectedStory) ?? null);
</script>

<aside class="inspector" aria-label="Story details">
  {#if !story}
    <p class="empty">Select a story.</p>
  {:else}
    <div class="head">
      <span class="id">{story.id}</span>
      <span class="status {story.status}">{story.status}</span>
    </div>
    <h2>{story.title}</h2>

    {#if story.description}
      <p class="desc">{story.description}</p>
    {/if}

    {#if story.criteria?.length}
      <div class="label">ACCEPTANCE</div>
      <ul>
        {#each story.criteria as c}
          <li>{c}</li>
        {/each}
      </ul>

      {#if !story.criteriaAreAuthoritative}
        <!-- chief ticks every checkbox as a side effect of marking a story done,
             so a completed story's checklist records that write and nothing
             else. Saying so is the honest thing; presenting it as evidence
             would not be. -->
        <p class="caveat">
          These were ticked automatically when the story was marked done. They are not a
          record of what was verified.
        </p>
      {/if}
    {/if}

    {#if story.branch || story.pr}
      <div class="label">BRANCH</div>
      {#if story.branch}<p class="mono">{story.branch}</p>{/if}
      {#if story.pr}
        <p class="mono">
          <a href={story.pr.url} target="_blank" rel="noreferrer">
            #{story.pr.number} {story.pr.state.toLowerCase()}{story.pr.draft ? " · draft" : ""}
          </a>
          {#if story.pr.base}<br />→ {story.pr.base}{/if}
        </p>
      {/if}
    {/if}

    {#if story.errors?.length}
      <div class="label danger">NEEDS ATTENTION</div>
      {#each story.errors as e}
        <p class="err">{e.message}{e.hint ? ` — ${e.hint}` : ""}</p>
      {/each}
    {/if}
  {/if}
</aside>

<style>
  .inspector {
    width: 320px;
    flex: none;
    border-left: 1px solid var(--border);
    background: var(--bg-panel);
    padding: 14px;
    overflow-y: auto;
  }

  .empty {
    color: var(--fg-3);
    margin: 0;
  }

  .head {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .id {
    font-family: var(--font-mono);
    font-size: 11.5px;
    color: var(--fg-3);
  }

  .status {
    font-size: 11px;
    padding: 1px 6px;
    border-radius: 3px;
    background: var(--bg-raised);
    color: var(--fg-3);
  }
  .status.done {
    color: var(--ok);
  }
  .status\:in-progress,
  .status.in-progress {
    color: var(--accent);
  }
  .status.blocked {
    color: var(--danger);
  }

  h2 {
    margin: 6px 0 10px;
    font-size: 15px;
    font-weight: 500;
    color: var(--fg-1);
  }

  .desc {
    margin: 0 0 14px;
    color: var(--fg-2);
  }

  .label {
    font-size: 11px;
    font-weight: 500;
    letter-spacing: 0.06em;
    color: var(--fg-3);
    margin: 14px 0 6px;
  }
  .label.danger {
    color: var(--danger);
  }

  ul {
    margin: 0;
    padding-left: 18px;
    color: var(--fg-2);
  }
  li {
    margin-bottom: 3px;
  }

  .caveat {
    margin: 8px 0 0;
    font-size: 11.5px;
    color: var(--warn);
    border-left: 2px solid var(--warn);
    padding-left: 8px;
  }

  .mono {
    font-family: var(--font-mono);
    font-size: 11.5px;
    color: var(--fg-2);
    margin: 0 0 4px;
    overflow-wrap: anywhere;
  }

  a {
    color: var(--accent);
    text-decoration: none;
  }
  a:hover {
    text-decoration: underline;
  }

  .err {
    color: var(--danger);
    font-size: 12px;
    margin: 0 0 6px;
  }
</style>
