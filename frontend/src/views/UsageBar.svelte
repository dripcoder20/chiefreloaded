<script lang="ts">
  import type { RunSnapshot } from "../platform";
  import type { UsageTotals } from "../stores/app.svelte";

  /**
   * Live usage summary for the status bar. Collapsed it shows the three headline
   * numbers — current story tokens, current session tokens, session cost — and
   * expands to a per-class breakdown. It is a disclosure widget so assistive
   * technology sees its expanded/collapsed state; every abbreviated value carries
   * the unabridged number as its accessible label.
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

  let expanded = $state(false);

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

  type Row = { label: string; field: Field };
  const breakdown = $derived<Row[]>([
    { label: "Input", field: tokenClass(session, "inputTokens", "input") },
    { label: "Output", field: tokenClass(session, "outputTokens", "output") },
    { label: "Cache read", field: tokenClass(session, "cacheReadTokens", "cache read") },
    { label: "Cache write", field: tokenClass(session, "cacheWriteTokens", "cache write") },
    { label: "Reasoning", field: tokenClass(session, "reasoningTokens", "reasoning") },
  ]);

  // A partially supported provider reports only some token classes; a class it
  // never reports sums to zero, which we show as unavailable rather than "0".
  function tokenClass(totals: UsageTotals | undefined, key: keyof UsageTotals, name: string): Field {
    const n = totals ? (totals[key] as number) : 0;
    if (!totals || n === 0) {
      return { available: false, text: UNAVAILABLE, label: `${name} tokens unavailable` };
    }
    return { available: true, text: compactTokens(n), label: `${name}: ${fullTokens(n)}` };
  }
</script>

{#if run}
  <div class="usage">
    {#if !hasUsage}
      <span class="waiting">Waiting for usage</span>
    {:else}
      <button
        class="toggle"
        aria-expanded={expanded}
        aria-label={`Usage summary, ${expanded ? "expanded" : "collapsed"}`}
        onclick={() => (expanded = !expanded)}
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
          <span class="v" class:muted={!sessionCost.available} aria-label={sessionCost.label}>
            {sessionCost.text}
          </span>
        </span>
        <span class="chevron" aria-hidden="true">{expanded ? "▾" : "▴"}</span>
      </button>

      {#if expanded}
        <div class="detail" role="region" aria-label="Session usage breakdown">
          {#each breakdown as row}
            <div class="row">
              <span class="k">{row.label}</span>
              <span class="v" class:muted={!row.field.available} aria-label={row.field.label}>
                {row.field.text}
              </span>
            </div>
          {/each}
          <div class="row">
            <span class="k">Cost</span>
            <span class="v" class:muted={!sessionCost.available} aria-label={sessionCost.label}>
              {sessionCost.text}
            </span>
          </div>
        </div>
      {/if}
    {/if}
  </div>
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

  .chevron {
    color: var(--fg-3);
    font-size: 9px;
  }

  .detail {
    position: absolute;
    bottom: calc(100% + 6px);
    right: 0;
    display: grid;
    gap: 4px 16px;
    min-width: 160px;
    padding: 8px 12px;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    box-shadow: 0 6px 24px rgba(0, 0, 0, 0.35);
    z-index: 20;
  }
  .detail .row {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 16px;
  }
</style>
