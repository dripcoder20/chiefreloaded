<script lang="ts">
  import LogView from "./LogView.svelte";
  import { app } from "../stores/app.svelte";
  import { droppedFor, logFor, storiesFor } from "../stores/logs.svelte";

  /**
   * The log as a panel under the story list rather than a view you switch to.
   *
   * Watching the agent and watching the stories tick over are the same activity,
   * and making them alternatives meant you were always on the wrong tab. Along
   * the bottom rather than in the right sidebar because agent output is wide —
   * file paths, shell commands, tool results — and a 320px column would wrap
   * nearly every line.
   */

  const STORAGE_KEY = "loop.logPanel";
  const MIN = 90;
  const COLLAPSED = 30;
  const DEFAULT = 260;

  type Persisted = { height: number; collapsed: boolean };

  function restore(): Persisted {
    try {
      const raw = localStorage.getItem(STORAGE_KEY);
      if (raw) {
        const v = JSON.parse(raw) as Partial<Persisted>;
        return {
          height: typeof v.height === "number" ? v.height : DEFAULT,
          collapsed: v.collapsed === true,
        };
      }
    } catch {
      // A corrupt or unavailable store is not worth failing the layout over.
    }
    return { height: DEFAULT, collapsed: false };
  }

  const initial = restore();
  let height = $state(initial.height);
  let collapsed = $state(initial.collapsed);
  let dragging = $state(false);

  $effect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({ height, collapsed }));
    } catch {
      // Persisting the layout is a convenience, never a requirement.
    }
  });

  const prd = $derived(app.selectedPrd ?? "");
  const stories = $derived(storiesFor(prd));
  const missing = $derived(droppedFor(prd));

  /**
   * Which story's output to show. "" is every event, including the run-level
   * chatter between stories, which carries no story of its own.
   *
   * It follows the running story on its own until the user picks one, because
   * the story you want to watch is almost always the one executing. Choosing
   * explicitly pins it — being yanked to another story mid-read is the same
   * irritation as being scrolled to the bottom mid-read.
   */
  let selected = $state("");
  let pinnedStory = $state(false);

  $effect(() => {
    const active = app.currentRun?.storyId;
    if (pinnedStory || !active || selected === active) return;
    selected = active;
  });

  // A story that has aged out of the ring can no longer be shown; fall back to
  // everything rather than rendering an empty panel with no explanation.
  $effect(() => {
    if (selected && !stories.includes(selected)) selected = "";
  });

  function chooseStory(event: Event): void {
    selected = (event.currentTarget as HTMLSelectElement).value;
    // Choosing "All" hands following back to the run.
    pinnedStory = selected !== "";
  }

  const count = $derived(logFor(prd, selected || undefined).length);

  /** Exposed so the keyboard map can toggle the panel with `t`. */
  export function toggle(): void {
    collapsed = !collapsed;
  }

  function startDrag(e: PointerEvent): void {
    if (collapsed) return;
    dragging = true;
    const startY = e.clientY;
    const startHeight = height;
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);

    const move = (ev: PointerEvent) => {
      // Dragging up grows the panel, which is the direction the handle moves.
      const next = startHeight - (ev.clientY - startY);
      // Leave the story list at least a couple of rows; a panel that can eat the
      // whole column is a panel you have to undo.
      const max = Math.max(MIN, window.innerHeight - 260);
      height = Math.min(max, Math.max(MIN, next));
    };
    const up = (ev: PointerEvent) => {
      dragging = false;
      (e.currentTarget as HTMLElement).releasePointerCapture(ev.pointerId);
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
    };

    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  }
</script>

<section class="panel" style="height: {collapsed ? COLLAPSED : height}px">
  <div
    class="handle"
    class:dragging
    class:disabled={collapsed}
    onpointerdown={startDrag}
    role="separator"
    aria-orientation="horizontal"
    aria-label="Resize the log"
  ></div>

  <header>
    <button class="toggle" onclick={toggle} aria-expanded={!collapsed}>
      <span class="chev" class:down={!collapsed}>›</span>
      Log
    </button>

    {#if !collapsed && stories.length > 0}
      <!-- A PRD's log is every story it has run concatenated; without this the
           only question you can answer is "what happened", never "what did this
           story do". -->
      <label class="scope">
        <span class="sr-only">Show log for</span>
        <select value={selected} onchange={chooseStory} aria-label="Show log for">
          <option value="">All stories</option>
          {#each stories as id (id)}
            <option value={id}>{id}</option>
          {/each}
        </select>
      </label>
    {/if}

    {#if count > 0}
      <span class="count tnum">{count.toLocaleString()}</span>
    {/if}
    {#if missing > 0}
      <span class="gap tnum" title="Events were dropped while the interface was behind">
        {missing.toLocaleString()} dropped
      </span>
    {/if}

    <span class="spacer"></span>
    <span class="hint">t</span>
  </header>

  {#if !collapsed}
    <div class="body">
      <LogView storyId={selected} />
    </div>
  {/if}
</section>

<style>
  .scope select {
    padding: 1px 4px;
    background: var(--bg-raised);
    color: var(--fg-2);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    font: inherit;
    font-size: 11px;
  }
  .scope select:hover {
    color: var(--fg-1);
  }

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

  .panel {
    position: relative;
    display: flex;
    flex-direction: column;
    flex: none;
    border-top: 1px solid var(--border);
    background: var(--bg-panel);
    min-height: 0;
  }

  /* A generous grab area over a hairline border: the visible edge is 1px, but
     nobody should have to aim at 1px. */
  .handle {
    position: absolute;
    top: -3px;
    left: 0;
    right: 0;
    height: 7px;
    cursor: ns-resize;
    z-index: 2;
  }
  .handle.disabled {
    cursor: default;
  }
  .handle:hover:not(.disabled)::after,
  .handle.dragging::after {
    content: "";
    position: absolute;
    top: 3px;
    left: 0;
    right: 0;
    height: 1px;
    background: var(--accent);
  }

  header {
    display: flex;
    align-items: center;
    gap: 10px;
    height: 29px;
    padding: 0 14px;
    flex: none;
    font-size: 11.5px;
    color: var(--fg-3);
  }

  .toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    background: none;
    border: 0;
    padding: 0;
    color: var(--fg-2);
    font: inherit;
    font-weight: 500;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    cursor: default;
  }
  .toggle:hover {
    color: var(--fg-1);
  }

  .chev {
    display: inline-block;
    transition: transform var(--dur-hover) var(--ease);
    font-size: 13px;
    line-height: 1;
  }
  .chev.down {
    transform: rotate(90deg);
  }

  .count {
    font-family: var(--font-mono);
  }

  .gap {
    font-family: var(--font-mono);
    color: var(--warn);
  }

  .spacer {
    flex: 1;
  }

  .hint {
    font-family: var(--font-mono);
    opacity: 0.6;
  }

  .body {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    position: relative;
  }
</style>
