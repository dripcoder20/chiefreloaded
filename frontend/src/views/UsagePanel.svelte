<script lang="ts">
  import { onMount, tick } from "svelte";
  import { api, type PRDDetail } from "../platform";
  import {
    app,
    contextLevel,
    contextUtilization,
    costLevel,
    type SessionUsage,
    type StoryUsage,
    type UsageGroup,
    type UsageTotals,
    type WarnLevel,
  } from "../stores/app.svelte";

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

  // ------------------------------------------------------ history browsing --

  // The General scope is a history browser: a list of retained sessions, each
  // expandable to its stories, and a story drill-down that shows the same usage
  // breakdown the other scopes render — without ever touching the active PRD or
  // the story selected in the main Stories view.
  const sessions = $derived(app.generalSessions);

  // Only the expanded session's stories are in the DOM at once, so the list stays
  // responsive with thousands of sessions and story aggregates.
  let expandedRun = $state<string | null>(null);

  // A selected historical story, shown as a full breakdown over the list.
  let historyStory = $state<{ session: SessionUsage; story: StoryUsage } | null>(null);

  // PRD details, fetched lazily on expand, purely to enrich stories with their
  // titles and completion states. A PRD that no longer resolves caches as null and
  // the story simply shows without a title — titles are shown "when available".
  let prdCache = $state<Record<string, PRDDetail | null>>({});

  function toggleSession(s: SessionUsage): void {
    expandedRun = expandedRun === s.runId ? null : s.runId;
    if (expandedRun && s.prd) void loadPrd(s.prd);
  }

  async function loadPrd(name: string): Promise<void> {
    if (name in prdCache) return;
    prdCache = { ...prdCache, [name]: null }; // mark in-flight so we fetch once
    try {
      prdCache = { ...prdCache, [name]: await api.prd.get(name) };
    } catch {
      // A deleted or unreadable PRD leaves the null placeholder: no title, no
      // completion state, which is the documented "when available" behaviour.
    }
  }

  function storyMeta(session: SessionUsage, storyId: string): { title?: string; status?: string } {
    const detail = session.prd ? prdCache[session.prd] : null;
    const story = detail?.stories?.find((s) => s.id === storyId);
    return { title: story?.title, status: story?.status };
  }

  function openHistoryStory(session: SessionUsage, story: StoryUsage): void {
    // Deliberately does NOT set app.selectedPrd / app.selectedStory: browsing
    // history must never disturb the main Stories view.
    historyStory = { session, story };
  }

  function backToList(): void {
    historyStory = null;
  }

  // A session's lifecycle state, as a badge: a distinct label and class so active
  // and interrupted sessions never read as completed, stopped or failed. The label
  // carries the meaning, so the state is never conveyed by colour alone (AC4).
  function stateBadge(state: string | undefined): { label: string; cls: string } {
    switch (state) {
      case "active":
        return { label: "Active", cls: "active" };
      case "interrupted":
        return { label: "Interrupted", cls: "interrupted" };
      case "completed":
        return { label: "Completed", cls: "completed" };
      case "stopped":
        return { label: "Stopped", cls: "stopped" };
      case "failed":
        return { label: "Failed", cls: "failed" };
      default:
        return { label: "Unknown", cls: "unknown" };
    }
  }

  const isEnded = (s: SessionUsage) => s.state !== "active";

  const dateFmt = new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  });

  function whenText(ms: number | undefined): string {
    if (!ms || ms <= 0) return "—";
    return dateFmt.format(new Date(ms));
  }

  // A scope's headline token total and cost, reused for session and story rows.
  function totalTokensText(t: UsageTotals | undefined): string {
    if (!t || t.records === 0) return "—";
    return `${compactFmt.format(t.totalTokens)}`;
  }

  function totalTokensLabel(t: UsageTotals | undefined): string {
    if (!t || t.records === 0) return "no usage recorded";
    return `${fullFmt.format(t.totalTokens)} tokens`;
  }

  function costText(t: UsageTotals | undefined): string {
    if (!t || !t.currency) return "—";
    return money(t.currency, t.cost);
  }

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

  // level marks a cell that has crossed a configured warning threshold. It is only
  // ever set where the underlying value is genuinely known, so an unavailable cell
  // (unknown window, quota or cost) never carries a warning.
  type Cell = { available: boolean; text: string; label: string; level?: WarnLevel };

  const UNAVAILABLE = "—"; // em dash

  const thresholds = $derived(app.thresholds);

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
    const amount = money(g.currency ?? "", g.cost);
    const level = costLevel(
      { currency: g.currency ?? "", cost: g.cost } as UsageTotals,
      thresholds,
    );
    const over = level === "warn" ? " — over the configured warning amount" : "";
    return { available: true, text: amount, label: `cost: ${amount}${over}`, level };
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
    const ratio = contextUtilization(g) ?? peak / window;
    const level = contextLevel(ratio, thresholds);
    const suffix =
      level === "critical" ? " — critical" : level === "warn" ? " — warning" : "";
    return {
      available: true,
      text: pctFmt.format(ratio),
      label: `context utilization: ${pctFmt.format(ratio)} of ${fullFmt.format(window)} tokens${suffix}`,
      level,
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
      // In a historical story drill-down, Escape steps back to the list first.
      if (historyStory) {
        backToList();
        return;
      }
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
    tabindex="-1"
  >
    <header class="head">
      <h2>Usage</h2>
      <button class="close" onclick={onclose} aria-label="Close usage panel">×</button>
    </header>

    <!-- The tabs themselves carry a roving tabindex, so the list is reached
         through them rather than being a tab stop of its own. -->
    <div class="tabs" role="tablist" aria-label="Usage scope" onkeydown={onTabKey} tabindex="-1">
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
      {#if scope === "general"}
        {@render generalScope()}
      {:else}
        {@render scopeGroups(activeGroups, active.label)}
      {/if}

      <p class="footnote">
        Provider account quotas, subscription balances and rate limits are not reported to
        Loop and are shown as unavailable — they never trigger a warning.
      </p>
    </div>
  </div>
</div>

<!-- Group breakdown, shared by the per-scope tabs and the historical story view. -->
{#snippet groupBlocks(groups: UsageGroup[])}
  {#if groups.length > 1}
    <p class="grouped-note">
      Grouped by provider, model and currency — totals are not combined across groups.
    </p>
  {/if}
  {#each groups as g}
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
              class:warn={row.cell.level === "warn"}
              class:critical={row.cell.level === "critical"}
              title={row.cell.label}
              aria-label={row.cell.label}
            >
              {#if row.cell.level === "warn" || row.cell.level === "critical"}
                <span class="warn-icon" aria-hidden="true">⚠</span>
              {/if}
              {row.cell.text}
              {#if row.tag}<em class="tag">{row.tag}</em>{/if}
            </span>
          </div>
        {/each}
      </div>
    </section>
  {/each}
{/snippet}

{#snippet scopeGroups(groups: UsageGroup[], label: string)}
  {#if groups.length === 0}
    <p class="empty">No usage recorded for this {label.toLowerCase()} yet.</p>
  {:else}
    {@render groupBlocks(groups)}
  {/if}
{/snippet}

<!-- The General scope: browse retained sessions and their stories. -->
{#snippet generalScope()}
  {#if sessions.length === 0}
    <p class="empty">No usage recorded for this project</p>
  {:else if historyStory}
    {@const s = historyStory.session}
    {@const st = historyStory.story}
    {@const meta = storyMeta(s, st.storyId)}
    <div class="history-detail">
      <button class="back" onclick={backToList}>← Back to sessions</button>
      <div class="detail-head">
        <span class="run-id">{s.runId}</span>
        <span class="story-id">{st.storyId}</span>
        {#if meta.title}<span class="story-title">{meta.title}</span>{/if}
      </div>
      <div class="detail-sub">
        {st.attempts}
        {st.attempts === 1 ? "attempt" : "attempts"}
        · {s.prd || "unknown PRD"}{meta.status ? ` · ${meta.status}` : ""}
      </div>
      {@render scopeGroups(groupsOf(st.totals), "story")}
    </div>
  {:else}
    <ul class="sessions" aria-label="Retained sessions, newest first">
      {#each sessions as s (s.runId)}
        {@const badge = stateBadge(s.state)}
        <li class="session">
          <button
            class="session-head"
            aria-expanded={expandedRun === s.runId}
            onclick={() => toggleSession(s)}
          >
            <span class="disclosure" aria-hidden="true">{expandedRun === s.runId ? "▾" : "▸"}</span>
            <span class="run-id">{s.runId}</span>
            <span class="prd">{s.prd || "unknown PRD"}</span>
            <span class="pm">{s.provider || "unknown"}{s.model ? ` · ${s.model}` : ""}</span>
            <span class="badge {badge.cls}">{badge.label}</span>
            <span class="when" title={`started ${whenText(s.startedAt)}`}>
              {whenText(s.startedAt)} → {isEnded(s) ? whenText(s.endedAt) : "active"}
            </span>
            <span class="tok" aria-label={totalTokensLabel(s.totals)}>{totalTokensText(s.totals)}</span>
            <span class="cost">{costText(s.totals)}</span>
          </button>

          {#if expandedRun === s.runId}
            <div class="stories">
              {#if s.stories && s.stories.length > 0}
                {#each s.stories as st (st.storyId)}
                  {@const meta = storyMeta(s, st.storyId)}
                  <button class="story-row" onclick={() => openHistoryStory(s, st)}>
                    <span class="story-id">{st.storyId}</span>
                    {#if meta.title}<span class="story-title">{meta.title}</span>{/if}
                    <span class="attempts">
                      {st.attempts}
                      {st.attempts === 1 ? "attempt" : "attempts"}
                    </span>
                    {#if meta.status}<span class="story-status">{meta.status}</span>{/if}
                    <span class="tok" aria-label={totalTokensLabel(st.totals)}>
                      {totalTokensText(st.totals)}
                    </span>
                    <span class="cost">{costText(st.totals)}</span>
                  </button>
                {/each}
              {:else}
                <p class="empty">No stories recorded for this session.</p>
              {/if}
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
{/snippet}

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

  .footnote {
    margin: 16px 0 0;
    padding-top: 10px;
    border-top: 1px solid var(--border);
    color: var(--fg-3);
    font-size: 11px;
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
  /* Warning colour is always paired with the ⚠ icon and a descriptive label, so
     the state is never conveyed by colour alone. */
  .v.warn {
    color: var(--warn);
  }
  .v.critical {
    color: var(--danger);
  }
  .warn-icon {
    margin-right: 3px;
    font-size: 11px;
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

  /* ------------------------------------------------------ history browser -- */

  .sessions {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .session + .session {
    border-top: 1px solid var(--border);
  }

  .session-head,
  .story-row {
    display: flex;
    align-items: baseline;
    gap: 8px;
    width: 100%;
    background: none;
    border: 0;
    padding: 6px 4px;
    color: var(--fg-2);
    font: inherit;
    font-family: var(--font-mono);
    font-size: 12px;
    text-align: left;
    cursor: default;
  }
  .session-head:hover,
  .session-head:focus-visible,
  .story-row:hover,
  .story-row:focus-visible {
    outline: none;
    background: var(--bg-inset, rgba(127, 127, 127, 0.08));
  }

  .disclosure {
    width: 1em;
    color: var(--fg-3);
  }
  .run-id {
    color: var(--fg-1);
    font-weight: 600;
  }
  .prd {
    color: var(--fg-2);
  }
  .pm {
    color: var(--fg-3);
  }
  .when {
    color: var(--fg-3);
    font-size: 11px;
  }
  /* Push the numeric columns to the right so rows line up. */
  .session-head .tok,
  .story-row .tok {
    margin-left: auto;
  }
  .tok {
    color: var(--fg-1);
  }
  .cost {
    color: var(--fg-2);
    min-width: 4em;
    text-align: right;
  }

  /* State badges: each has a distinct label AND colour, so active/interrupted are
     never distinguished from completed/stopped/failed by colour alone. */
  .badge {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 0 5px;
  }
  .badge.active {
    color: var(--accent);
    border-color: var(--accent);
  }
  .badge.interrupted {
    color: var(--warn);
    border-color: var(--warn);
  }
  .badge.completed {
    color: var(--ok, #3fa66a);
    border-color: var(--ok, #3fa66a);
  }
  .badge.stopped {
    color: var(--fg-3);
  }
  .badge.failed {
    color: var(--danger);
    border-color: var(--danger);
  }
  .badge.unknown {
    color: var(--fg-3);
  }

  .stories {
    padding: 2px 0 6px 1.6em;
  }
  .story-row {
    font-size: 11px;
  }
  .story-id {
    color: var(--fg-1);
    font-weight: 600;
  }
  .story-title {
    color: var(--fg-2);
  }
  .attempts,
  .story-status {
    color: var(--fg-3);
  }

  .history-detail .back {
    background: none;
    border: 0;
    padding: 0 0 8px;
    color: var(--fg-3);
    font: inherit;
    cursor: default;
  }
  .history-detail .back:hover,
  .history-detail .back:focus-visible {
    outline: none;
    color: var(--fg-1);
  }
  .detail-head {
    display: flex;
    align-items: baseline;
    gap: 8px;
    font-family: var(--font-mono);
  }
  .detail-sub {
    margin: 2px 0 10px;
    color: var(--fg-3);
    font-family: var(--font-mono);
    font-size: 11px;
  }
</style>
