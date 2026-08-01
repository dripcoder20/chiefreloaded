# Usage reporting

Loop attributes every agent CLI's reported token and cost usage to the story,
session and project it belongs to, aggregates it, persists it, and surfaces it
live in the status bar and the usage panel.

This document defines the three scopes, lists what each supported provider
reports (and what it does not), and states plainly how displayed cost should be
read. It is the reference the end-to-end tests and the frontend are verified
against; keep it in the same commit as any change to usage behaviour.

## Scopes

Usage rolls up at four scopes. Three are surfaced in the UI as tabs; the fourth
(attempt) is the unit the others are built from.

| Scope | What it covers | Keyed by |
|---|---|---|
| **Story** | One story across every attempt, including retries and failed attempts. | `runId/storyId` |
| **Session** | One run from start to finish. Pausing and resuming keep the same run, so its usage keeps accumulating. | `runId` |
| **General** | The whole project: the grand total across every run, plus the browsable session/story history. | project |
| _Attempt_ | A single agent invocation. Not shown directly; it is what Story and Session sum over. | `runId/storyId#n` |

A story that is retried at the run level records usage under each attempt it
spends; the Story scope sums them, and the per-attempt scopes keep them
separate. A failed attempt still reports the usage it spent before it failed —
usage is attributed whether or not the attempt landed a commit.

Totals are **absolute cumulative roll-ups**, never deltas. A reconnecting or
replaying client adopts the numbers wholesale, so redelivery is idempotent and
never double-counts.

### General history

The General scope also browses retained sessions, newest first, each expandable
to its stories. A session carries a lifecycle state:

- **active** / **interrupted** — derived from the live runs: a run currently
  executing is active; one with recorded usage but no live run and no recorded
  terminal outcome was interrupted.
- **completed** / **stopped** / **failed** — recorded terminal outcomes,
  persisted to disk so they survive a restart.

## Supported fields by provider

Token classes and cost come straight from each agent CLI's own structured
output; Loop never estimates or infers a value a provider did not report. A
field a provider omits is shown as unavailable (`—`), never as a misleading
zero.

| Field | Claude | Cursor | Codex | OpenCode |
|---|:-:|:-:|:-:|:-:|
| Input tokens | ✅ | ✅ | ✅ | ✅ |
| Output tokens | ✅ | ✅ | ✅ | ✅ |
| Cache-read tokens | ✅ | ✅ | ✅ | ✅ |
| Cache-write tokens | ✅ | ✅ | — | ✅ |
| Reasoning tokens | — | — | — | ✅ |
| Total tokens | derived | derived | derived | derived |
| Reported cost | ✅ | ✅ | — | ✅ |
| Model | ✅ | ✅ | — | — |
| Context window | — | — | — | — |

Field notes and known limitations:

- **Claude** — usage rides on assistant messages and is finalised on the
  `result` line (`input_tokens`, `output_tokens`, `cache_read_input_tokens`,
  `cache_creation_input_tokens`, `total_cost_usd`, `model`). No reasoning-token
  or context-window figure is reported.
- **Cursor** — the Cursor agent is Claude-based and its `result` line mirrors
  Claude's usage shape, so it is parsed identically. Same limitations as Claude.
  There is no local Cursor fixture; revisit if a real payload diverges.
- **Codex** — `turn.completed.usage` reports `input_tokens`, `output_tokens`,
  and `cached_input_tokens` (recorded as cache-read). It reports **no cost, no
  reasoning tokens, no cache-write tokens, and no model**, so cost and those
  classes are shown as unavailable for Codex sessions.
- **OpenCode** — `step_finish` reports the fullest set: `input`, `output`,
  `reasoning`, `cache.read`, `cache.write`, and `cost`. It reports **no model
  identifier**, so the model column is unavailable.
- **Total tokens** — no provider reports a single total token count. Loop
  derives it from the reported components rather than double-counting cache or
  reasoning tokens; if a provider ever reports its own total, that value wins.
- **Context window** — no provider CLI reports the model's context-window size
  today, so context utilization (peak single payload ÷ window) is shown as
  unavailable. It becomes live automatically once a provider populates it.
- **Account quotas, subscription balances, and rate limits** are not reported by
  any provider and have no field in the model. They are never shown and never
  drive a warning.

## Reading the cost figure

**Displayed cost is informational only.** It is a convenience number for
noticing unusual spend, not a billing record, and it never pauses, stops, or
otherwise controls a run.

Loop distinguishes two kinds of cost and labels every cost figure accordingly:

- **Reported** — the cost the agent CLI itself emitted (e.g. Claude's
  `total_cost_usd`). This is what every provider that reports a cost emits today.
- **Estimated** — a cost Loop derived from token counts and a pricing table.
  The model supports this kind and it is labelled distinctly, but no provider
  path currently produces an estimated cost, so today every reported cost is
  **Reported**. When a scope mixes reported and estimated figures its cost is
  labelled **Mixed**.

Cost is only shown when a provider actually reported one — a currency is
attached in that case. A provider that reports tokens but no cost (Codex) shows
its cost as **Unavailable** rather than as `$0.00`, so an unknown cost can never
be mistaken for a free one. A genuinely reported `$0.00` still displays as such.

Because cost figures can be absent, mixed-currency, or estimated, they are never
summed blindly across providers: a scope with more than one provider, model, or
currency is shown group by group rather than combined into one misleading total.

## Verification

Usage behaviour is covered end to end:

- `e2e/usage_e2e_test.go` runs a scripted agent through multiple stories and a
  retried story, then reads the live report back through `loopctl usage -json`
  and asserts the General (project), Session (run), Story, and per-attempt
  totals. A second test asserts that after a fresh process cold-starts the same
  project, the completed history reloads from `.chief/usage.json`.
- `frontend/src/stores/usage.test.ts` covers the presentation states: loading,
  waiting, partial-data, mixed-provider, warning, critical, empty-history, and
  persistence-error.
