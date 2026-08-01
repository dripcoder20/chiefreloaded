<script lang="ts">
  import { app } from "../stores/app.svelte";
  import type { StorySnap } from "../platform";

  const stories = $derived(app.detail?.stories ?? []);

  function icon(s: StorySnap): string {
    switch (s.status) {
      case "done":
        return "✓";
      case "in-progress":
        return "●";
      case "blocked":
        return "!";
      default:
        return "○";
    }
  }
</script>

<div class="list" role="listbox" aria-label="User stories" tabindex="-1">
  {#if stories.length === 0}
    <p class="empty">
      This PRD has no user stories yet. Create them with <code>chief new</code>.
    </p>
  {/if}

  {#each stories as story (story.id)}
    <button
      class="row"
      class:selected={app.selectedStory === story.id}
      role="option"
      aria-selected={app.selectedStory === story.id}
      onclick={() => (app.selectedStory = story.id)}
    >
      <span class="icon {story.status}">{icon(story)}</span>
      <span class="id tnum">{story.id}</span>
      <span class="title">{story.title}</span>
    </button>

    <!-- Per-story branch and pull-request state, the thing the TUI has no room
         for. Only rendered once a branch exists, so ordinary runs stay quiet. -->
    {#if story.branch || story.pr}
      <div class="stack-line">
        {#if story.branch}<span class="branch">⎇ {story.branch}</span>{/if}
        {#if story.pr}
          <a class="pr" href={story.pr.url} target="_blank" rel="noreferrer">
            #{story.pr.number}{story.pr.draft ? " draft" : ""}
          </a>
        {/if}
      </div>
    {/if}
  {/each}
</div>

<style>
  .list {
    overflow-y: auto;
    flex: 1;
    padding: 4px 0;
  }

  .empty {
    color: var(--fg-3);
    padding: 12px 14px;
    margin: 0;
  }

  .row {
    display: flex;
    align-items: baseline;
    gap: 10px;
    width: 100%;
    padding: 5px 14px;
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

  .row.selected {
    background: var(--bg-raised);
    border-left-color: var(--accent);
    color: var(--fg-1);
  }

  .icon {
    width: 1em;
    color: var(--fg-3);
  }
  .icon.done {
    color: var(--ok);
  }
  .icon.in-progress {
    color: var(--accent);
  }
  .icon.blocked {
    color: var(--danger);
  }

  .id {
    font-family: var(--font-mono);
    font-size: 11.5px;
    color: var(--fg-3);
    flex: none;
  }

  .title {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .stack-line {
    display: flex;
    gap: 12px;
    padding: 0 14px 4px 48px;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
  }

  .pr {
    color: var(--accent);
    text-decoration: none;
  }
  .pr:hover {
    text-decoration: underline;
  }
</style>
