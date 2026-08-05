<script lang="ts">
  import { app, storyMeta, metaText } from "../stores/app.svelte";
  import type { StorySnap } from "../platform";
  import PrLink from "./PrLink.svelte";
  import { formatTokens, summarise, type StoryRow } from "../stores/summary";
  import { contextUtilization } from "../stores/usage";

  const stories = $derived(app.detail?.stories ?? []);

  /**
   * Per-story spend, folded across every run of this PRD.
   *
   * The same derivation the Summary tab uses, rather than a second one that
   * could disagree with it — two views of the same story reporting different
   * token counts is the kind of thing that makes people stop trusting both.
   */
  const rows = $derived(new Map(summarise(app.detail, app.usage, app.runs).stories.map((r) => [r.id, r])));

  /**
   * How full the context window got, per story, as a fraction.
   *
   * The peak across every group, since what matters is the closest the agent
   * came to the limit, not its average. A provider that never reports a window
   * size yields nothing at all rather than a guess — the whole point of the
   * measure is that it is the model's real ceiling.
   */
  const contextByStory = $derived.by(() => {
    const peaks = new Map<string, number>();
    for (const session of app.usage?.sessions ?? []) {
      for (const story of session.stories ?? []) {
        for (const group of story.totals.groups ?? []) {
          const ratio = contextUtilization(group);
          if (ratio === null) continue;
          peaks.set(story.storyId, Math.max(peaks.get(story.storyId) ?? 0, ratio));
        }
      }
    }
    return peaks;
  });

  /**
   * The run implementing the selected PRD, which is where the active story's
   * agent and model come from. Reading the session rather than the configured
   * defaults is the point: a session may run an override or a provider-selected
   * fallback, so a default would be a plausible wrong answer.
   */
  const run = $derived(app.currentRun);

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

    <!-- The agent and model of the session implementing this story. Live state
         while it runs; once it has finished, the row below reports what the
         usage ledger recorded, which is what survives. -->
    {@const meta = storyMeta(run, story.id)}
    {#if meta}
      <div class="session-meta">
        <span class="meta-item" class:soft={meta.agent.kind !== "value"}>
          Agent: <span class="meta-value" title={metaText(meta.agent)}>{metaText(meta.agent)}</span>
        </span>
        <span class="meta-item" class:soft={meta.model.kind !== "value"}>
          Model: <span class="meta-value" title={metaText(meta.model)}>{metaText(meta.model)}</span>
        </span>
      </div>
    {/if}

    <!-- What this story actually cost and where it landed. Every part is
         omitted when there is nothing to say, so a story that has never run
         adds no line at all and the list stays a list. -->
    {@const row = rows.get(story.id)}
    {@const context = contextByStory.get(story.id)}
    {#if row && (row.attempts > 0 || row.branch || row.pr)}
      <div class="stack-line">
        {#if row.attempts > 0}
          <span title="Attempts that spent tokens">{row.attempts}×</span>
          <span class="tnum" title="Tokens across every attempt">
            {formatTokens(row.totalTokens)}
          </span>
          {#if row.cost !== undefined}
            <span class="tnum" title="Reported cost">
              {row.cost.toLocaleString(undefined, {
                style: "currency",
                currency: app.usage?.project.currency || "USD",
              })}
            </span>
          {/if}
          {#if context !== undefined}
            <span class="tnum" title="Peak context-window use">
              ctx {Math.round(context * 100)}%
            </span>
          {/if}
          {#if !meta && row.model}
            <span title={row.provider ? `${row.provider} · ${row.model}` : row.model}>
              {row.model}
            </span>
          {/if}
        {/if}
        {#if row.branch}<span class="branch" title={row.branch}>⎇ {row.branch}</span>{/if}
        {#if row.pr}<PrLink pr={row.pr} now={app.now} />{/if}
      </div>
    {/if}
  {/each}
</div>

<style>
  /* Sits under the story it belongs to, near its status, without displacing the
     title or the row's own controls. Values truncate visually; the full text
     stays available through the title attribute and to assistive technology. */
  .session-meta {
    display: flex;
    gap: 12px;
    padding: 0 14px 6px 38px;
    font-size: 11px;
    color: var(--fg-3);
  }

  .meta-item {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .meta-value {
    color: var(--fg-2);
    font-family: var(--font-mono);
  }

  /* Resolving… and Unavailable read as pending, not as failure — a story with
     an unresolved model has not gone wrong. */
  .soft .meta-value {
    color: var(--fg-3);
    font-style: italic;
    font-family: inherit;
  }

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

  /* Wraps rather than scrolls: a story with a long branch name and a pull
     request must not push the count and cost out of reach. */
  .stack-line {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 4px 12px;
    padding: 0 14px 5px 48px;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
  }

  .stack-line .branch {
    max-width: 34ch;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
