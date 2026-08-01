<script lang="ts">
  import type { RunSnapshot } from "../platform";
  import {
    app,
    costLevel,
    worstContext,
    type UsageTotals,
    type WarnLevel,
  } from "../stores/app.svelte";
  import UsagePanel from "./UsagePanel.svelte";

  /**
   * Live usage summary for the status bar. It shows the three headline numbers —
   * current story tokens, current session tokens, session cost — and activating
   * it opens the detailed usage panel over the current view without navigating
   * away. It is a disclosure trigger so assistive technology sees the panel's
   * open/closed state; every abbreviated value carries the unabridged number as
   * its accessible label. When the panel closes, focus returns here.
   */
  let {
    run,
    session,
    story,
  }: {
    run: RunSnapshot | null;
    session: UsageTotals | undefined;
    story: UsageTotals | undefined;
  } = $props();

  let panelOpen = $state(false);
  let triggerEl = $state<HTMLButtonElement | null>(null);

  function openPanel(): void {
    panelOpen = true;
  }

  function closePanel(): void {
    panelOpen = false;
    // Return focus to the trigger, as a modal dialog must.
    triggerEl?.focus();
  }

  // A run reports usage once its first attempt does; until then the slice is
  // absent or empty. That is the "Waiting for usage" state, distinct from having
  // no run at all (nothing rendered).
  const hasUsage = $derived(!!session && session.records > 0);

  const compactFmt = new Intl.NumberFormat(undefined, {
    notation: "compact",
    maximumFractionDigits: 1,
  });
  const fullFmt = new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 });

  // Lowercased so a locale's "12.4K" reads as the "12.4k" the design calls for;
  // only the magnitude suffix is affected, never the digits.
  function compactTokens(n: number): string {
    return `${compactFmt.format(n).toLowerCase()} tokens`;
  }

  function fullTokens(n: number): string {
    return `${fullFmt.format(n)} tokens`;
  }

  function moneyFmt(currency: string, notation: "compact" | "standard"): Intl.NumberFormat {
    return new Intl.NumberFormat(undefined, { style: "currency", currency, notation });
  }

  type Field = { available: boolean; text: string; label: string };

  const UNAVAILABLE = "—"; // em dash

  function tokenField(totals: UsageTotals | undefined, name: string): Field {
    if (!totals || totals.records === 0) {
      return { available: false, text: UNAVAILABLE, label: `${name} tokens unavailable` };
    }
    return {
      available: true,
      text: compactTokens(totals.totalTokens),
      label: `${name}: ${fullTokens(totals.totalTokens)}`,
    };
  }

  // Cost is only meaningful once a provider has reported one; the currency stays
  // empty otherwise. A zero-but-reported cost would still carry a currency, so we
  // never mistake "cost unknown" for "$0.00".
  function costField(totals: UsageTotals | undefined): Field {
    if (!totals || !totals.currency) {
      return { available: false, text: "Unavailable", label: "session cost unavailable" };
    }
    return {
      available: true,
      text: moneyFmt(totals.currency, "compact").format(totals.cost),
      label: `session cost: ${moneyFmt(totals.currency, "standard").format(totals.cost)}`,
    };
  }

  const storyTokens = $derived(tokenField(story, "current story"));
  const sessionTokens = $derived(tokenField(session, "current session"));
  const sessionCost = $derived(costField(session));

  // Warnings are informational only — they highlight, never pause or stop a run.
  // Context utilization is computed only where a model window is known, so an
  // unknown quota or window produces no warning rather than a false one.
  const pctFmt = new Intl.NumberFormat(undefined, {
    style: "percent",
    maximumFractionDigits: 0,
  });

  const sessionContext = $derived(worstContext(session, app.thresholds));
  const costWarn = $derived(costLevel(session, app.thresholds));

  // The cost value is styled at the cost warning level; a breach never rises past
  // "warn" because an over-budget session is a heads-up, not a hard limit.
  const costState = $derived<WarnLevel>(sessionCost.available ? costWarn : "none");

  type Badge = { level: WarnLevel; text: string; label: string };

  // A single context badge for the status bar, present only when a threshold is
  // crossed. The icon and text carry the meaning; colour merely reinforces it.
  const contextBadge = $derived<Badge | null>(badgeFor(sessionContext));

  function badgeFor(ctx: { level: WarnLevel; ratio: number | null }): Badge | null {
    if (ctx.level === "none" || ctx.ratio == null) return null;
    const pct = pctFmt.format(ctx.ratio);
    const word = ctx.level === "critical" ? "critical" : "warning";
    return {
      level: ctx.level,
      text: `${pct} context`,
      label: `Context ${word}: session context is at ${pct} of the model window`,
    };
  }
</script>

{#if run}
  <div class="usage">
    {#if !hasUsage}
      <span class="waiting">Waiting for usage</span>
    {:else}
      <button
        class="toggle"
        bind:this={triggerEl}
        aria-haspopup="dialog"
        aria-expanded={panelOpen}
        aria-label="Open usage details"
        onclick={openPanel}
      >
        <span class="pair">
          <span class="k">story</span>
          <span class="v" class:muted={!storyTokens.available} aria-label={storyTokens.label}>
            {storyTokens.text}
          </span>
        </span>
        <span class="pair">
          <span class="k">session</span>
          <span class="v" class:muted={!sessionTokens.available} aria-label={sessionTokens.label}>
            {sessionTokens.text}
          </span>
        </span>
        <span class="pair">
          <span class="k">cost</span>
          <span
            class="v"
            class:muted={!sessionCost.available}
            class:warn={costState === "warn"}
            aria-label={costState === "warn"
              ? `${sessionCost.label} — over the configured warning amount`
              : sessionCost.label}
          >
            {sessionCost.text}
          </span>
        </span>
        {#if contextBadge}
          <span
            class="badge"
            class:warn={contextBadge.level === "warn"}
            class:critical={contextBadge.level === "critical"}
            aria-label={contextBadge.label}
          >
            <span class="badge-icon" aria-hidden="true">⚠</span>
            <span class="badge-text">{contextBadge.text}</span>
          </span>
        {/if}
        <span class="chevron" aria-hidden="true">▴</span>
      </button>
    {/if}
  </div>
{/if}

{#if panelOpen}
  <UsagePanel onclose={closePanel} />
{/if}

<style>
  .usage {
    position: relative;
    display: flex;
    align-items: center;
    min-width: 0;
    flex: 0 1 auto;
    font-family: var(--font-mono);
  }

  .waiting {
    color: var(--fg-3);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .toggle {
    display: flex;
    align-items: center;
    gap: 12px;
    min-width: 0;
    padding: 1px 6px;
    background: none;
    border: 1px solid transparent;
    border-radius: var(--radius-control);
    color: var(--fg-3);
    font: inherit;
    cursor: default;
  }
  .toggle:hover,
  .toggle:focus-visible {
    color: var(--fg-1);
    border-color: var(--border);
    outline: none;
  }

  .pair {
    display: inline-flex;
    align-items: baseline;
    gap: 5px;
    min-width: 0;
    white-space: nowrap;
  }

  .k {
    color: var(--fg-3);
    opacity: 0.75;
  }
  .v {
    color: var(--fg-2);
  }
  .v.muted {
    color: var(--fg-3);
    opacity: 0.7;
  }
  .v.warn {
    color: var(--warn);
  }

  /* The badge pairs colour with an icon and text, so the warning never relies on
     colour alone and reads for assistive tech via its aria-label. */
  .badge {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 0 6px;
    border-radius: var(--radius-control);
    border: 1px solid currentColor;
    white-space: nowrap;
    font-size: 11px;
  }
  .badge.warn {
    color: var(--warn);
  }
  .badge.critical {
    color: var(--danger);
  }
  .badge-icon {
    font-size: 10px;
  }

  .chevron {
    color: var(--fg-3);
    font-size: 9px;
  }
</style>
