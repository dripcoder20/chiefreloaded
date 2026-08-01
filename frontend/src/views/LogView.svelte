<script lang="ts">
  import { app, toolArgument } from "../stores/app.svelte";
  import { droppedFor, logFor } from "../stores/logs.svelte";
  import { EventKind, type LoopEvent } from "../platform";

  const events = $derived(logFor(app.selectedPrd ?? ""));
  const missing = $derived(droppedFor(app.selectedPrd ?? ""));

  let viewport = $state<HTMLDivElement | null>(null);
  /** Auto-scroll disengages the moment the user scrolls up, and only re-engages
   *  when they come back to the bottom. Yanking someone back down while they are
   *  reading is the single most irritating thing a log view can do. */
  let pinned = $state(true);
  let unseen = $state(0);

  function onScroll(): void {
    if (!viewport) return;
    const distance = viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight;
    pinned = distance < 32;
    if (pinned) unseen = 0;
  }

  function toBottom(): void {
    if (!viewport) return;
    viewport.scrollTop = viewport.scrollHeight;
    pinned = true;
    unseen = 0;
  }

  // Re-run whenever the buffer version changes.
  $effect(() => {
    const count = events.length;
    if (!viewport) return;
    if (pinned) {
      // After paint, or the new row is not measured yet.
      requestAnimationFrame(() => {
        if (viewport && pinned) viewport.scrollTop = viewport.scrollHeight;
      });
    } else {
      unseen = count - lastSeen;
    }
    lastSeen = count;
  });

  let lastSeen = 0;

  function rowClass(ev: LoopEvent): string {
    switch (ev.kind) {
      case EventKind.EvAgentText:
        return "text";
      case EventKind.EvAgentTool:
      case EventKind.EvAgentToolResult:
        return "tool";
      case EventKind.EvStoryDone:
        return "done";
      case EventKind.EvStoryFailed:
      case EventKind.EvRunError:
        return "error";
      case EventKind.EvAgentRetry:
      case EventKind.EvAgentWatchdog:
        return "warn";
      case EventKind.EvGit:
        return ev.git?.state === "error" ? "error" : ev.git?.state === "warn" ? "warn" : "git";
      default:
        return "meta";
    }
  }

  function label(ev: LoopEvent): string {
    switch (ev.kind) {
      case EventKind.EvAgentTool:
        return `${ev.agent?.tool ?? "tool"} ${toolArgument(ev)}`;
      case EventKind.EvAgentToolResult:
        return `↳ ${ev.text ?? ""}`;
      case EventKind.EvStoryStarted:
        return `▶ ${ev.storyId} ${ev.text ?? ""}`;
      case EventKind.EvStoryDone:
        return `✓ ${ev.storyId} ${ev.text ?? ""}`;
      case EventKind.EvStorySkipped:
        return `— ${ev.storyId} ${ev.text ?? ""}`;
      case EventKind.EvStoryFailed:
        return `✗ ${ev.storyId} ${ev.text ?? ""}`;
      case EventKind.EvGit:
        return `git ${ev.git?.op ?? ""} ${ev.text ?? ""}`;
      case EventKind.EvRunComplete:
        return "🎉 all stories complete";
      case EventKind.EvRunError:
        return `✗ ${ev.text ?? ""}`;
      case EventKind.EvAgentRetry:
        return `↻ retry ${ev.agent?.retryCount ?? 0}/${ev.agent?.retryMax ?? 0}`;
      case EventKind.EvAgentWatchdog:
        return "⏱ the agent went quiet and was restarted";
      default:
        return ev.text ?? ev.kind;
    }
  }
</script>

<div class="log" bind:this={viewport} onscroll={onScroll}>
  {#if missing > 0}
    <!-- Never let a gap pass silently: a partial log that looks complete is
         worse than one that admits what it is missing. -->
    <p class="gap">
      {missing.toLocaleString()} event{missing === 1 ? "" : "s"} were dropped while the
      interface was behind. The log below is incomplete.
    </p>
  {/if}

  {#if events.length === 0}
    <p class="empty">No output yet. Press <kbd>s</kbd> to start the loop.</p>
  {/if}

  {#each events as ev (ev.seq)}
    <div class="row {rowClass(ev)}">{label(ev)}</div>
  {/each}
</div>

{#if !pinned && unseen > 0}
  <button class="pill" onclick={toBottom}>
    ↓ {unseen.toLocaleString()} new
  </button>
{/if}

<style>
  .log {
    flex: 1;
    overflow-y: auto;
    padding: 8px 0;
    font-family: var(--font-mono);
    font-size: 12.5px;
    line-height: 20px;
  }

  .row {
    padding: 0 14px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    color: var(--fg-2);
  }

  .row.tool {
    color: var(--info);
  }
  .row.done {
    color: var(--ok);
  }
  .row.error {
    color: var(--danger);
  }
  .row.warn {
    color: var(--warn);
  }
  .row.git {
    color: var(--accent);
  }
  .row.meta {
    color: var(--fg-3);
  }

  .empty,
  .gap {
    padding: 10px 14px;
    margin: 0;
    font-family: var(--font-ui);
  }
  .empty {
    color: var(--fg-3);
  }
  .gap {
    color: var(--warn);
    border-left: 2px solid var(--warn);
  }

  .pill {
    position: absolute;
    right: 20px;
    bottom: 16px;
    padding: 5px 12px;
    background: var(--accent);
    color: var(--n-0);
    border: 0;
    border-radius: 999px;
    font: 500 12px var(--font-ui);
    cursor: default;
  }

  kbd {
    font-family: var(--font-mono);
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 0 4px;
  }
</style>
