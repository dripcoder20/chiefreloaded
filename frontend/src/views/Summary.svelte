<script lang="ts">
  import { app } from "../stores/app.svelte";
  import { formatDuration, formatTokens, summarise } from "../stores/summary";
  import PrLink from "./PrLink.svelte";

  /**
   * What happened while this PRD was implemented.
   *
   * Assembled from what outlives the process — prd.md, the usage ledger, the
   * PRD's sidecar — rather than from the log, whose ring drops old events and
   * starts empty after a restart. That is the whole point: the question "how did
   * this go" is usually asked long after the run, and a summary that could only
   * answer it while the run was still on screen would answer it never.
   */

  const report = $derived(summarise(app.detail, app.usage, app.runs));
  const money = $derived(report.totals?.currency || "USD");

  function cost(value: number | undefined): string {
    if (value === undefined) return "—";
    return value.toLocaleString(undefined, { style: "currency", currency: money });
  }
</script>

<div class="summary">
  <div class="content">
    {#if !app.detail}
      <p class="muted">No PRD selected.</p>
    {:else}
      <section class="cards">
        <div class="card">
          <span class="label">Stories</span>
          <span class="value tnum">{report.counts.done}/{report.counts.total}</span>
          <span class="sub">
            {#if report.counts.blocked > 0}{report.counts.blocked} blocked{:else if report.counts.inProgress > 0}{report
                .counts.inProgress} in progress{:else}{report.counts.todo} to go{/if}
          </span>
        </div>

        <div class="card">
          <span class="label">Time</span>
          <span class="value tnum">{formatDuration(report.activeMs)}</span>
          <!-- Two numbers because they answer different questions: how long the
               agent was working, and how long the PRD has been underway. -->
          <span class="sub">{formatDuration(report.elapsedMs)} elapsed</span>
        </div>

        <div class="card">
          <span class="label">Tokens</span>
          <span class="value tnum">{formatTokens(report.totals?.totalTokens ?? 0)}</span>
          <span class="sub">{report.sessions.length} run{report.sessions.length === 1 ? "" : "s"}</span>
        </div>

        <div class="card">
          <span class="label">Cost</span>
          <span class="value tnum">{cost(report.totals?.cost)}</span>
          <span class="sub">{report.totals?.records ?? 0} reports</span>
        </div>
      </section>

      {#if report.failures.length > 0}
        <section>
          <h3>Failures</h3>
          <ul class="failures">
            {#each report.failures as failure (failure.runId + (failure.storyId ?? ""))}
              <li>
                {#if failure.storyId}<span class="story-id tnum">{failure.storyId}</span>{/if}
                <span class="message">{failure.message}</span>
                {#if failure.hint}<span class="hint">{failure.hint}</span>{/if}
                <!-- A recorded failure is all that survives a restart: the run
                     ended badly, but its error message did not outlive it. -->
                {#if failure.source === "recorded"}<span class="tag">recorded</span>{/if}
              </li>
            {/each}
          </ul>
        </section>
      {/if}

      <section>
        <h3>Stories</h3>
        {#if report.stories.length === 0}
          <p class="muted">This PRD has no user stories yet.</p>
        {:else}
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Story</th>
                  <th class="num">Tries</th>
                  <th class="num">Time</th>
                  <th class="num">Tokens</th>
                  <th class="num">Cost</th>
                  <th>Model</th>
                  <th>Branch</th>
                  <th>PR</th>
                </tr>
              </thead>
              <tbody>
                {#each report.stories as story (story.id)}
                  <tr>
                    <td>
                      <span class="dot {story.status}" title={story.status}></span>
                      <span class="story-id tnum">{story.id}</span>
                      <span class="title">{story.title}</span>
                    </td>
                    <td class="num tnum">{story.attempts || "—"}</td>
                    <td class="num tnum">{formatDuration(story.spanMs)}</td>
                    <td class="num tnum">{formatTokens(story.totalTokens)}</td>
                    <td class="num tnum">{cost(story.cost)}</td>
                    <td class="mono">{story.model ?? "—"}</td>
                    <td class="mono">{story.branch ?? "—"}</td>
                    <td>{#if story.pr}<PrLink pr={story.pr} now={app.now} />{:else}—{/if}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}

        {#if !report.hasHistory}
          <p class="muted note">
            No run has spent anything on this PRD yet, so the columns above have nothing
            to report.
          </p>
        {/if}
      </section>
    {/if}
  </div>
</div>

<style>
  /* Full width so its scrollbar sits at the pane edge; the table needs the room. */
  .summary {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
  }

  .content {
    padding: 16px 18px;
  }

  section {
    margin-bottom: 22px;
  }

  h3 {
    margin: 0 0 10px;
    font-size: 12px;
    font-weight: 500;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--fg-3);
  }

  .cards {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
    gap: 10px;
  }

  .card {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 10px 12px;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
  }

  .label {
    font-size: 11px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--fg-3);
  }

  .value {
    font-size: 18px;
    color: var(--fg-1);
  }

  .sub {
    font-size: 11px;
    color: var(--fg-3);
  }

  /* The table is the one thing here that can outgrow the pane, so it scrolls
     inside itself rather than making the whole view scroll sideways. */
  .table-wrap {
    overflow-x: auto;
  }

  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }

  th {
    text-align: left;
    font-weight: 500;
    color: var(--fg-3);
    padding: 4px 10px 6px 0;
    border-bottom: 1px solid var(--border);
    white-space: nowrap;
  }

  td {
    padding: 5px 10px 5px 0;
    border-bottom: 1px solid var(--border);
    color: var(--fg-2);
    white-space: nowrap;
  }

  .num {
    text-align: right;
    padding-right: 14px;
  }

  .mono {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
  }

  .title {
    color: var(--fg-1);
  }

  .story-id {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
    margin-right: 8px;
  }

  .dot {
    display: inline-block;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    margin-right: 8px;
    background: var(--fg-3);
  }
  .dot.done {
    background: var(--ok);
  }
  .dot.in-progress {
    background: var(--accent);
  }
  .dot.blocked {
    background: var(--danger);
  }

  .failures {
    list-style: none;
    margin: 0;
    padding: 0;
    font-size: 12px;
  }
  .failures li {
    display: flex;
    align-items: baseline;
    gap: 8px;
    padding: 3px 0;
    color: var(--warn);
  }
  .message {
    color: var(--fg-2);
  }
  .hint,
  .tag {
    color: var(--fg-3);
    font-size: 11px;
  }

  .muted {
    color: var(--fg-3);
  }
  .note {
    margin: 10px 0 0;
    font-size: 11.5px;
  }
</style>
