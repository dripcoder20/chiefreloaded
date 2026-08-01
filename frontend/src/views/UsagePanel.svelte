<script lang="ts">
  import { onMount, tick } from "svelte";
  import { app, type UsageGroup, type UsageTotals } from "../stores/app.svelte";

  /**
   * Detailed usage panel. It opens over the current view — the run keeps going
   * behind it — and breaks usage down by scope (Story, Session, General) and,
   * within a scope, by provider/model/currency group so mixed usage is never
   * combined into one misleading figure. Estimated and reported costs are
   * labeled distinctly, and every value a provider does not report is shown as
   * unavailable with the provider named and a concise reason.
   *
   * It is a modal dialog: focus is trapped inside it, Escape dismisses it, and
   * the caller returns focus to the status-bar trigger afterwards.
   */
  let { onclose }: { onclose: () => void } = $props();

  type Scope = "story" | "session" | "general";
  let scope = $state<Scope>("story");

  let dialogEl = $state<HTMLDivElement | null>(null);

  const run = $derived(app.currentRun);
  const projectName = $derived(app.project?.name ?? "project");

  type ScopeDef = { key: Scope; label: string; name: string; totals: UsageTotals | undefined };

  // The three scopes, each with a name that identifies exactly which story, run
  // or project the numbers belong to.
  const scopes = $derived<ScopeDef[]>([
    {
      key: "story",
      label: "Story",
      name: storyName(),
      totals: app.currentUsage.story,
    },
    {
      key: "session",
      label: "Session",
      name: run ? `Run ${run.id}` : "No active run",
      totals: app.currentUsage.session,
    },
    { key: "general", label: "General", name: projectName, totals: app.generalUsage },
  ]);

  const active = $derived(scopes.find((s) => s.key === scope) ?? scopes[0]);

  function storyName(): string {
    if (!run?.storyId) return "No current story";
    const title = app.currentStoryTitle;
    return title ? `${run.storyId} — ${title}` : run.storyId;
  }

  // A scope with no groups (older data, or an empty scope) still renders as a
  // single group synthesized from the flat totals, so the panel never blanks.
  function groupsOf(totals: UsageTotals | undefined): UsageGroup[] {
    if (!totals || totals.records === 0) return [];
    if (totals.groups && totals.groups.length > 0) return totals.groups;
    return [syntheticGroup(totals)];
  }

  function syntheticGroup(t: UsageTotals): UsageGroup {
    return {
      records: t.records,
      inputTokens: t.inputTokens,
      outputTokens: t.outputTokens,
      reasoningTokens: t.reasoningTokens,
      cacheReadTokens: t.cacheReadTokens,
      cacheWriteTokens: t.cacheWriteTokens,
      totalTokens: t.totalTokens,
      cost: t.cost,
      hasCost: t.cost > 0 || !!t.currency,
      currency: t.currency,
    };
  }

  const activeGroups = $derived(groupsOf(active.totals));

  // ---------------------------------------------------------------- format --

  const compactFmt = new Intl.NumberFormat(undefined, {
    notation: "compact",
    maximumFractionDigits: 1,
  });
  const fullFmt = new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 });
  const pctFmt = new Intl.NumberFormat(undefined, {
    style: "percent",
    maximumFractionDigits: 1,
  });

  function money(currency: string, cost: number): string {
    const code = currency || "USD";
    return new Intl.NumberFormat(undefined, { style: "currency", currency: code }).format(cost);
  }

  function providerLabel(g: UsageGroup): string {
    return g.provider || "unknown provider";
  }

  type Cell = { available: boolean; text: string; label: string };

  const UNAVAILABLE = "—"; // em dash

  // A token class that never appeared in the group sums to zero; we surface that
  // as unavailable with the provider named, rather than a misleading "0".
  function tokenCell(g: UsageGroup, value: number, name: string): Cell {
    if (value <= 0) {
      return unavailable(`${providerLabel(g)}: ${name} not reported`);
    }
    return {
      available: true,
      text: `${compactFmt.format(value)}`,
      label: `${name}: ${fullFmt.format(value)} tokens`,
    };
  }

  function costCell(g: UsageGroup): Cell {
    if (!g.hasCost) {
      return unavailable(`${providerLabel(g)}: no cost reported`);
    }
    return {
      available: true,
      text: money(g.currency ?? "", g.cost),
      label: `cost: ${money(g.currency ?? "", g.cost)}`,
    };
  }

  // Reported vs estimated is a required distinction: an estimate must never read
  // as a provider-billed figure. Mixed groups say so plainly.
  function costKindLabel(g: UsageGroup): string {
    if (!g.hasCost) return "";
    if (g.costKind === "estimated") return "Estimated";
    if (g.costKind === "mixed") return "Mixed";
    return "Reported";
  }

  function contextWindowCell(g: UsageGroup): Cell {
    if (!g.contextWindow || g.contextWindow <= 0) {
      return unavailable(`${providerLabel(g)}: context-window size not reported`);
    }
    return {
      available: true,
      text: `${compactFmt.format(g.contextWindow)}`,
      label: `context window: ${fullFmt.format(g.contextWindow)} tokens`,
    };
  }

  // Utilization needs both a window and a peak payload; without the window it is
  // unknowable, which is the common case today.
  function utilizationCell(g: UsageGroup): Cell {
    const window = g.contextWindow ?? 0;
    const peak = g.peakContextTokens ?? 0;
    if (window <= 0) {
      return unavailable(`${providerLabel(g)}: context-window size not reported`);
    }
    if (peak <= 0) {
      return unavailable(`${providerLabel(g)}: no payload size to compare`);
    }
    const ratio = peak / window;
    return {
      available: true,
      text: pctFmt.format(ratio),
      label: `context utilization: ${pctFmt.format(ratio)} of ${fullFmt.format(window)} tokens`,
    };
  }

  function unavailable(reason: string): Cell {
    return { available: false, text: UNAVAILABLE, label: reason };
  }

  type Row = { label: string; cell: Cell; tag?: string };

  // Every supported dimension for a group, in the order the panel lists them.
  // Provider and model are shown in the group header, not as rows.
  function rowsOf(g: UsageGroup): Row[] {
    return [
      { label: "Input", cell: tokenCell(g, g.inputTokens, "input") },
      { label: "Output", cell: tokenCell(g, g.outputTokens, "output") },
      { label: "Reasoning", cell: tokenCell(g, g.reasoningTokens, "reasoning") },
      { label: "Cache read", cell: tokenCell(g, g.cacheReadTokens, "cache read") },
      { label: "Cache write", cell: tokenCell(g, g.cacheWriteTokens, "cache write") },
      { label: "Total", cell: tokenCell(g, g.totalTokens, "total") },
      { label: "Cost", cell: costCell(g), tag: costKindLabel(g) },
      { label: "Context window", cell: contextWindowCell(g) },
      { label: "Context utilization", cell: utilizationCell(g) },
    ];
  }

  // ------------------------------------------------------------- keyboard --

  const order: Scope[] = ["story", "session", "general"];

  function onTabKey(e: KeyboardEvent): void {
    const i = order.indexOf(scope);
    if (e.key === "ArrowRight" || e.key === "ArrowDown") {
      selectByIndex((i + 1) % order.length);
    } else if (e.key === "ArrowLeft" || e.key === "ArrowUp") {
      selectByIndex((i - 1 + order.length) % order.length);
    } else if (e.key === "Home") {
      selectByIndex(0);
    } else if (e.key === "End") {
      selectByIndex(order.length - 1);
    } else {
      return;
    }
    e.preventDefault();
  }

  async function selectByIndex(i: number): Promise<void> {
    scope = order[i];
    await tick();
    (dialogEl?.querySelector<HTMLElement>(`#usage-tab-${scope}`))?.focus();
  }

  // Trap Tab within the dialog so keyboard focus can never wander behind it, and
  // dismiss on Escape.
  function onDialogKey(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      onclose();
      return;
    }
    if (e.key !== "Tab" || !dialogEl) return;
    const focusables = dialogEl.querySelectorAll<HTMLElement>(
      'button, [href], [tabindex]:not([tabindex="-1"])',
    );
    if (focusables.length === 0) return;
    const first = focusables[0];
    const last = focusables[focusables.length - 1];
    const activeEl = document.activeElement as HTMLElement | null;
    if (e.shiftKey && activeEl === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && activeEl === last) {
      e.preventDefault();
      first.focus();
    }
  }

  onMount(() => {
    (dialogEl?.querySelector<HTMLElement>(`#usage-tab-${scope}`))?.focus();
  });
</script>

<div
  class="scrim"
  role="presentation"
  onclick={(e) => {
    if (e.target === e.currentTarget) onclose();
  }}
>
  <div
    class="panel"
    role="dialog"
    aria-modal="true"
    aria-label="Usage details"
    bind:this={dialogEl}
    onkeydown={onDialogKey}
  >
    <header class="head">
      <h2>Usage</h2>
      <button class="close" onclick={onclose} aria-label="Close usage panel">×</button>
    </header>

    <div class="tabs" role="tablist" aria-label="Usage scope" onkeydown={onTabKey}>
      {#each scopes as s}
        <button
          id={`usage-tab-${s.key}`}
          role="tab"
          aria-selected={scope === s.key}
          tabindex={scope === s.key ? 0 : -1}
          class:on={scope === s.key}
          onclick={() => (scope = s.key)}
        >
          {s.label}
        </button>
      {/each}
    </div>

    <div class="scope-name" aria-live="polite">{active.name}</div>

    <div class="body" role="tabpanel" aria-label={`${active.label} usage`}>
      {#if activeGroups.length === 0}
        <p class="empty">No usage recorded for this {active.label.toLowerCase()} yet.</p>
      {:else}
        {#if activeGroups.length > 1}
          <p class="grouped-note">
            Grouped by provider, model and currency — totals are not combined across groups.
          </p>
        {/if}
        {#each activeGroups as g}
          <section class="group">
            <div class="group-head">
              <span class="provider">{providerLabel(g)}</span>
              <span class="model">{g.model || "unknown model"}</span>
              {#if g.currency}<span class="currency">{g.currency}</span>{/if}
            </div>

            <div class="grid">
              {#each rowsOf(g) as row}
                <div class="cell">
                  <span class="k">{row.label}</span>
                  <span
                    class="v"
                    class:muted={!row.cell.available}
                    title={row.cell.label}
                    aria-label={row.cell.label}
                  >
                    {row.cell.text}
                    {#if row.tag}<em class="tag">{row.tag}</em>{/if}
                  </span>
                </div>
              {/each}
            </div>
          </section>
        {/each}
      {/if}
    </div>
  </div>
</div>

<style>
  .scrim {
    position: fixed;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    background: rgba(0, 0, 0, 0.4);
    z-index: 100;
  }

  .panel {
    display: flex;
    flex-direction: column;
    width: min(560px, 92vw);
    max-height: 80vh;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: 8px;
    box-shadow: 0 16px 48px rgba(0, 0, 0, 0.5);
    overflow: hidden;
  }

  .head {
    display: flex;
    align-items: center;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
  }
  .head h2 {
    margin: 0;
    font-size: 14px;
    font-weight: 600;
    color: var(--fg-1);
  }
  .close {
    margin-left: auto;
    background: none;
    border: 0;
    color: var(--fg-3);
    font-size: 18px;
    line-height: 1;
    cursor: default;
  }
  .close:hover,
  .close:focus-visible {
    color: var(--fg-1);
    outline: none;
  }

  .tabs {
    display: flex;
    gap: 4px;
    padding: 6px 12px 0;
  }
  .tabs button {
    background: none;
    border: 0;
    border-bottom: 2px solid transparent;
    padding: 6px 10px;
    color: var(--fg-3);
    font: inherit;
    cursor: default;
  }
  .tabs button.on {
    color: var(--fg-1);
    border-bottom-color: var(--accent);
  }
  .tabs button:focus-visible {
    outline: none;
    color: var(--fg-1);
  }

  .scope-name {
    padding: 4px 16px 8px;
    color: var(--fg-2);
    font-family: var(--font-mono);
    font-size: 12px;
    border-bottom: 1px solid var(--border);
  }

  .body {
    padding: 12px 16px 16px;
    overflow-y: auto;
  }

  .empty {
    margin: 8px 0;
    color: var(--fg-3);
  }

  .grouped-note {
    margin: 0 0 10px;
    color: var(--fg-3);
    font-size: 12px;
  }

  .group + .group {
    margin-top: 14px;
    padding-top: 12px;
    border-top: 1px dashed var(--border);
  }

  .group-head {
    display: flex;
    align-items: baseline;
    gap: 8px;
    margin-bottom: 8px;
    font-family: var(--font-mono);
  }
  .provider {
    color: var(--fg-1);
    font-weight: 600;
  }
  .model {
    color: var(--fg-2);
    font-size: 12px;
  }
  .currency {
    color: var(--fg-3);
    font-size: 11px;
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 0 4px;
  }

  .grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 6px 20px;
  }
  .cell {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
    font-family: var(--font-mono);
    font-size: 12px;
  }
  .k {
    color: var(--fg-3);
  }
  .v {
    color: var(--fg-1);
  }
  .v.muted {
    color: var(--fg-3);
    opacity: 0.7;
  }
  .tag {
    margin-left: 6px;
    font-style: normal;
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--fg-3);
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 0 4px;
  }
</style>
