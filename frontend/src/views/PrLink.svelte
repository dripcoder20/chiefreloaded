<script lang="ts">
  import type { PRRef } from "../platform";

  /**
   * A pull request, with how much to trust what it says beside it.
   *
   * GitHub is the source of truth and Loop only ever holds an answer it was
   * given at some point, so the state shown — open, merged, draft — is exactly
   * as current as the last successful lookup. A link that silently claims a
   * merged pull request is still open is worse than one that admits its age,
   * which is why nothing here renders a state without also carrying its date.
   */
  let { pr, now = Date.now() }: { pr: PRRef; now?: number } = $props();

  /** Beyond this, a remembered state is old enough to say so. */
  const FRESH_MS = 5 * 60 * 1000;

  const age = $derived(pr.checkedAt ? now - pr.checkedAt : Number.POSITIVE_INFINITY);
  const stale = $derived(age > FRESH_MS);

  const label = $derived.by(() => {
    const state = (pr.state ?? "").toUpperCase();
    if (state === "MERGED") return "merged";
    if (state === "CLOSED") return "closed";
    return pr.draft ? "draft" : "open";
  });

  const checked = $derived.by(() => {
    if (!pr.checkedAt) return "never confirmed against GitHub";
    if (age < 60_000) return "checked just now";
    if (age < 3_600_000) return `checked ${Math.round(age / 60_000)}m ago`;
    if (age < 86_400_000) return `checked ${Math.round(age / 3_600_000)}h ago`;
    return `checked ${Math.round(age / 86_400_000)}d ago`;
  });
</script>

<a
  class="pr"
  class:stale
  href={pr.url}
  target="_blank"
  rel="noreferrer"
  title="{pr.head ? `${pr.head} — ` : ''}{checked}"
>
  <span class="number tnum">#{pr.number}</span>
  <span class="state {label}">{label}</span>
</a>

<style>
  .pr {
    display: inline-flex;
    align-items: baseline;
    gap: 5px;
    color: var(--accent);
    text-decoration: none;
  }
  .pr:hover {
    text-decoration: underline;
  }

  .number {
    font-family: var(--font-mono);
  }

  .state {
    font-size: 10.5px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--fg-3);
  }
  .state.open {
    color: var(--ok);
  }
  .state.merged {
    color: var(--accent);
  }
  .state.closed {
    color: var(--fg-3);
  }

  /* The link keeps its weight — it still goes to the right place. Only the
     state fades, because that is the part which may have moved on. */
  .stale .state {
    opacity: 0.55;
    font-style: italic;
  }
</style>
