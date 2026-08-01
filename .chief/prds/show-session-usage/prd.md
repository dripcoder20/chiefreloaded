# PRD: Show Session Usage

## 1. Introduction/Overview

Add persistent usage information to Loop's status bar so users can understand how much agent capacity and money a run is consuming without leaving the main workflow. The feature must collect usage reported by supported agent CLIs, attribute it to the correct story and run, retain completed usage history, and present current-story, current-session, and project-wide totals.

For this PRD, the usage scopes are defined as follows:

- **Story usage:** The sum of all agent attempts for one story within a specific run, including failed or retried attempts.
- **Session usage:** The sum of all stories and attempts belonging to one Loop run ID, from start until the run reaches a terminal state. Pausing and resuming does not create a new session.
- **General usage:** The cumulative retained usage for all implementation runs in the currently open project, across PRDs and application restarts.

The status-bar control and its detailed panel must expose all usage dimensions supplied by the active provider: input tokens, output tokens, reasoning tokens, cache-read tokens, cache-write tokens, total tokens, context-window utilization, and monetary cost. A value that the provider does not report must be displayed as unavailable and must not be treated as zero. Provider-reported cost is authoritative; calculated cost is allowed only when an applicable, versioned price definition exists and must be labeled as an estimate.

## 2. Goals

- Show current-story and current-session token totals in the existing bottom status bar within one second of receiving a usage update.
- Let users open a detailed breakdown and switch among story, session, and general project scopes in no more than two interactions.
- Preserve completed session and story usage across application restarts and project reopen operations.
- Accurately aggregate every usage record exactly once, including usage from retries and failed attempts.
- Display provider-supported costs, context limits, and warning states without inventing unavailable values.
- Provide browsable usage history by run and story for the currently open project.
- Maintain correct behavior for Claude, Codex, OpenCode, and Cursor when their CLI output contains usage data, while degrading clearly when a provider or CLI version does not expose a field.

## 3. User Stories

### US-001: Normalize provider usage output
**Status:** todo
**Priority:** 1
**Description:** As a developer, I want agent-provider usage output normalized into one model so that all later aggregation and UI behavior use consistent data.

**Acceptance Criteria:**
- [ ] The shared usage model can represent input, output, reasoning, cache-read, cache-write, and total token counts as independently optional non-negative integers.
- [ ] The model can represent provider-reported cost, estimated cost, currency, context-window size, and the model identifier used for the request.
- [ ] Claude, Codex, OpenCode, and Cursor parsers extract every usage field present in their supported structured output fixtures.
- [ ] A missing provider field remains unavailable; it is not converted to `0`.
- [ ] Malformed usage payloads do not terminate an active run and produce a diagnosable warning event or log entry.
- [ ] Parser tests cover complete, partial, missing, malformed, and duplicate usage payloads for each provider.

### US-002: Attribute and aggregate usage
**Priority:** 2
**Description:** As a user, I want usage assigned to the correct attempt, story, and session so that the displayed totals reflect the work that consumed it.

**Acceptance Criteria:**
- [ ] Each normalized usage record carries a stable record ID or equivalent deduplication key, run ID, PRD name, story ID when applicable, attempt number, provider, model when reported, and timestamp.
- [ ] Usage emitted during an agent attempt is attributed to that attempt's active story and run.
- [ ] Retried and failed attempts remain included in story and session totals.
- [ ] Pausing and resuming a run continues accumulating against the same session.
- [ ] Replaying the same usage event or reconnecting the frontend does not increase any aggregate twice.
- [ ] Aggregate tests verify story totals, session totals, general project totals, retries, failures, pause/resume behavior, and duplicate delivery.

### US-003: Persist project usage history
**Priority:** 3
**Description:** As a user, I want usage history to survive restarts so that general totals and past sessions remain useful over time.

**Acceptance Criteria:**
- [ ] Usage records and the metadata required to group them by run, PRD, story, provider, and model are stored under the open project's `.chief/` directory.
- [ ] Writes are atomic so an interrupted write cannot replace valid history with a partial file.
- [ ] Reopening a project restores completed and interrupted session usage without starting a run.
- [ ] Opening a different project replaces the visible general total and history with that project's data.
- [ ] Missing persisted usage data is treated as an empty history.
- [ ] Invalid persisted data produces a visible, actionable error while leaving the PRD list and run controls usable.
- [ ] Persistence tests cover save/load, restart recovery, project switching, missing files, invalid files, and concurrent usage updates.

### US-004: Expose usage through session state and events
**Priority:** 4
**Description:** As a frontend developer, I want usage aggregates available through Loop's snapshot and ordered event stream so that the status bar stays current and recovers from dropped events.

**Acceptance Criteria:**
- [ ] The session read model exposes current story usage, current session usage, and general project usage.
- [ ] A non-coalescible usage event is emitted after a usage record is accepted and aggregated.
- [ ] Usage events identify the run and story to which the update belongs.
- [ ] A fresh snapshot contains totals equal to those obtained by applying all accepted usage events.
- [ ] Event replay followed by live events produces the same totals as a fresh snapshot and never double-counts usage.
- [ ] Browser-development mock data includes active and historical usage for complete and partially supported providers.

### US-005: Show live usage in the status bar
**Priority:** 5
**Description:** As a user, I want key usage values continuously visible in the existing bottom status bar so that I can monitor consumption while an agent works.

**Acceptance Criteria:**
- [ ] When a PRD has an active or most-recent run, the existing bottom status bar shows current-story total tokens, current-session total tokens, and session cost when available.
- [ ] Status-bar values update within one second of a usage update without requiring a page refresh or closing the log panel.
- [ ] Token and currency values use compact, locale-aware formatting while the accessible label exposes the unabridged value; for example, `12.4k tokens` has an accessible value of `12,431 tokens`.
- [ ] Unavailable fields display `—` or `Unavailable`, never `$0.00` or `0 tokens`.
- [ ] When no usage has yet been reported for a new run, the status bar displays `Waiting for usage`.
- [ ] The status bar remains legible at the application's supported minimum window width and does not obscure the activity ticker, running-count indicator, or keyboard-shortcut help.
- [ ] The usage summary is keyboard focusable and exposes its expanded/collapsed state to assistive technology.

### US-006: Inspect usage by scope
**Priority:** 6
**Description:** As a user, I want a detailed usage panel for story, session, and general scopes so that I can understand where usage came from.

**Acceptance Criteria:**
- [ ] Activating the status-bar usage summary opens a panel without navigating away from the Stories view or interrupting the run.
- [ ] The panel provides Story, Session, and General scope controls and clearly names the selected story, run, or project.
- [ ] Each scope shows every supported dimension: input, output, reasoning, cache read, cache write, total tokens, cost, provider, model, context-window size, and context utilization.
- [ ] For mixed providers, models, or currencies, totals are grouped rather than combined into a misleading single value.
- [ ] Estimated costs are labeled `Estimated`; provider-reported costs are labeled `Reported`.
- [ ] The panel explains unavailable values with the provider name and a concise reason when known.
- [ ] The panel can be opened, operated, and dismissed using only the keyboard; focus returns to the status-bar trigger when dismissed.

### US-007: Warn about approaching limits and unusual spend
**Priority:** 7
**Description:** As a user, I want clear usage warnings so that I can intervene before an agent reaches a known limit or exceeds an expected cost.

**Acceptance Criteria:**
- [ ] Context utilization is calculated only when both the relevant token count and model context-window size are known.
- [ ] The status bar and detail panel show a warning at 80% context utilization and a critical state at 95% by default.
- [ ] Users can configure context warning and critical percentages and an optional per-session cost-warning amount in Settings.
- [ ] The critical threshold must be greater than the warning threshold, and invalid settings cannot be saved.
- [ ] A warning is informational and never automatically pauses or stops a run.
- [ ] Unknown provider account quotas, subscription balances, or rate limits are labeled unavailable and do not generate false warnings.
- [ ] Warning colors are accompanied by text or an icon with an accessible label and meet the application's existing contrast requirements.

### US-008: Browse session and story history
**Priority:** 8
**Description:** As a user, I want to inspect prior usage by session and story so that I can compare completed work and identify expensive stories.

**Acceptance Criteria:**
- [ ] The General scope lists retained sessions newest first with run ID, PRD, provider/model, start time, end time or active state, token total, and cost when available.
- [ ] Expanding a session lists its stories with story ID, title when available, attempts, token total, cost, and completion state.
- [ ] Selecting a historical story shows its full usage-dimension breakdown without changing the active PRD or selected story in the main Stories view.
- [ ] Active and interrupted sessions are visibly distinguished from completed, stopped, and failed sessions.
- [ ] An empty project shows an explicit `No usage recorded for this project` state.
- [ ] History rendering remains responsive with at least 1,000 sessions and 10,000 story aggregates in a test fixture.

### US-009: Verify and document usage behavior
**Priority:** 9
**Description:** As a maintainer, I want end-to-end coverage and provider documentation so that usage reporting remains trustworthy as agent CLIs change.

**Acceptance Criteria:**
- [ ] An end-to-end test runs a scripted agent through multiple stories and attempts, then verifies live status-bar, session, story, and general totals.
- [ ] An end-to-end restart test verifies that completed usage history reloads from disk.
- [ ] Frontend tests cover loading, waiting, partial-data, mixed-provider, warning, critical, empty-history, and persistence-error states.
- [ ] Documentation defines Story, Session, and General scopes and lists the supported fields and known limitations for each provider.
- [ ] Documentation states that displayed cost is informational and distinguishes reported from estimated cost.
- [ ] Go tests, frontend type checking, frontend tests, and the project's end-to-end suite pass.

## 4. Functional Requirements

- **FR-1:** The system must normalize provider usage into optional fields for input, output, reasoning, cache-read, cache-write, and total tokens; reported cost; estimated cost; currency; context-window size; provider; and model.
- **FR-2:** The system must preserve the distinction between an unavailable usage field and a reported value of zero.
- **FR-3:** The system must assign every accepted usage record to exactly one run and, when emitted during a story attempt, exactly one story and attempt.
- **FR-4:** Story usage must include all attempts for that story within the run, including failed and retried attempts.
- **FR-5:** Session usage must include all usage records assigned to the run across pause and resume cycles until the run ends.
- **FR-6:** General usage must include all retained implementation-run usage for the currently open project across PRDs and application restarts.
- **FR-7:** The system must deduplicate repeated usage records before updating aggregates or persisted history.
- **FR-8:** Usage history must persist under the project's `.chief/` directory using atomic replacement and a versioned storage schema.
- **FR-9:** Switching projects must switch the loaded history and general aggregate; usage from different projects must never be combined.
- **FR-10:** The session snapshot must contain sufficient usage state to rebuild the frontend after startup or an unrecoverable event-stream gap.
- **FR-11:** Accepted usage updates must be published as ordered, non-coalescible session events.
- **FR-12:** The existing bottom status bar must continuously display current-story tokens, current-session tokens, and current-session cost when available.
- **FR-13:** Activating the status-bar usage control must open a detail panel with Story, Session, and General scopes.
- **FR-14:** The detail panel must display each available token category, cost provenance, provider, model, context-window size, and context utilization.
- **FR-15:** When an aggregate contains multiple providers, models, or currencies, the system must show separate groups and must not create a cross-currency cost total.
- **FR-16:** Provider-reported cost must take precedence over an estimate for the same usage record.
- **FR-17:** Estimated cost may be shown only when the provider, model, effective pricing version, and applicable token categories are known; otherwise cost must be unavailable.
- **FR-18:** Context utilization must be calculated only from a known context-window size and the provider-defined token count relevant to that window.
- **FR-19:** The system must show default context warnings at 80% and critical warnings at 95%, with user-configurable valid thresholds.
- **FR-20:** The system must support an optional user-configured session-cost warning and must not pause or stop a run when it is crossed.
- **FR-21:** The system must never present an inferred account quota, subscription balance, or rate limit as provider-reported data.
- **FR-22:** The General scope must list sessions newest first and permit drill-down from session to story aggregates.
- **FR-23:** Usage history must identify active, completed, stopped, failed, and interrupted sessions.
- **FR-24:** All compact status-bar values must have accessible full-value labels, and the detail panel must support keyboard-only operation and focus restoration.
- **FR-25:** A usage parsing or persistence failure must not crash the application or stop an otherwise healthy run; it must produce a visible, actionable warning.

## 5. Non-Goals (Out of Scope)

- Automatically pausing, stopping, throttling, or changing an agent model because of usage or cost.
- Purchasing credits, changing provider billing settings, or managing subscriptions from Loop.
- Claiming access to provider account quotas, remaining subscription balance, billing invoices, or rate-limit state when the CLI does not report them.
- Guaranteeing exact billing parity for estimated costs; provider billing remains authoritative.
- Importing historical usage from provider dashboards or CLI sessions that did not run through Loop.
- Aggregating usage across different projects into an application-wide or cloud-synchronized account total.
- Exporting usage history to CSV, JSON, or external analytics systems in the first release.
- Deleting or editing individual historical usage records in the first release.
- Including interactive PRD-authoring sessions in General usage; this release covers implementation runs started from Loop's run toolbar.
- Changing attempt budgets or PRD priorities automatically based on usage.

## 6. Design Considerations

- Reuse the existing bottom `.statusbar` in `frontend/src/App.svelte`; usage should be integrated alongside the activity ticker, running count, and keyboard hints rather than added to the central run-control toolbar.
- Keep the always-visible summary compact: current story tokens, session tokens, and session cost. Put token-category details, limit status, and history in an expandable panel.
- The panel should open upward from the status bar as a popover or anchored drawer so users can continue watching stories and logs while it is visible.
- Use the application's tabular-number styling for rapidly changing numeric values to prevent visual jitter.
- Preserve the activity ticker and running-count indicator at narrow widths. Keyboard hints may collapse before usage data; less important usage fields may then collapse into the detail trigger.
- Use `—` consistently for unavailable numeric fields, plus a text explanation in the detailed view.
- Warnings must use iconography/text in addition to color. Suggested labels are `Approaching context limit`, `Context limit critical`, and `Session cost warning`.
- Historical selection inside the usage panel must be local to that panel; it must not unexpectedly change the PRD or story selected in the main application.

## 7. Technical Considerations

- Loop currently receives structured streaming output from Claude, Codex, OpenCode, and Cursor through provider-specific parsers. Some parsers currently discard terminal/result messages that may contain usage; normalization belongs at this provider boundary.
- The current `RunSnapshot` and `StorySnap` models contain timing and run state but no usage. Usage must join the same snapshot/event recovery architecture so a frontend reconnect cannot lose or double-count totals.
- Finished runs currently live only in the in-memory session. Persistent usage history therefore needs a project-local, versioned store separate from transient run goroutines. The chosen filename and schema version should be documented and should remain compatible with chief's `.chief/` directory.
- Persist raw normalized records or an equivalently auditable structure in addition to aggregates so schema migrations and aggregation corrections do not require guessing from totals.
- Monetary values must use decimal-safe integer minor units or a decimal representation; binary floating-point must not be used for persisted currency arithmetic.
- Timestamps must use the project's existing Unix-millisecond convention at the frontend boundary.
- Total-token derivation must follow provider semantics. If a provider supplies a total, retain it; if the total is derived, record that provenance and avoid double-counting cache/reasoning categories that are already included in input or output.
- Pricing data, if bundled, must be versioned and keyed by provider and model with effective dates. A record must use the price effective at its timestamp. Unknown or ambiguous model aliases produce unavailable cost.
- Provider output formats change over time. Parser fixtures should record representative supported CLI output while excluding secrets, prompts, and generated content not needed to test usage.
- Usage records and history must not persist prompt text, response text, tool arguments, API keys, or provider credentials.
- General-history queries should return summaries first and load detailed records only when required so a large history does not block initial rendering.

## 8. Success Metrics

- In automated tests, 100% of valid usage fixture records are attributed to the expected run, story, and attempt and are counted exactly once.
- Live status-bar values appear or update within one second of the backend accepting a usage record.
- A user can reach any of the Story, Session, or General breakdowns from the main view in two interactions or fewer.
- After an application restart, story, session, and general totals exactly match the pre-restart persisted totals in end-to-end tests.
- The history panel becomes interactive within 200 ms for a fixture containing 1,000 sessions and 10,000 story aggregates on a supported development machine.
- No UI state displays an unavailable cost, quota, token category, or context limit as zero.
- No usage parsing or persistence error causes an active implementation run to stop in integration tests.
- All status-bar and detail-panel operations pass keyboard-navigation checks and expose full numeric values to assistive technology.

## 9. Open Questions

- Which exact CLI versions and output event shapes should define the initial support matrix for Claude, Codex, OpenCode, and Cursor?
- What project-local filename should hold versioned usage history, and should Loop add it to `.chief/.gitignore` automatically when `.chief/` is otherwise tracked?
- Should pricing definitions ship with Loop, be user-configurable, or both? This decision determines when calculated cost can be offered reliably.
- How should context utilization be defined for providers that report cumulative session tokens but do not report the tokens in the current model context?
- Should a later release include PRD-authoring agent sessions in General usage or present them as a separate usage category?
