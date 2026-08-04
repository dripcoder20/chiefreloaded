# PRD: UI Improvements

## 1. Introduction/Overview

Improve Loop's PRD authoring and implementation workflow by preserving active authoring sessions across tab changes, making common PRD and repository actions easier to discover through a Claude-style PRD sidebar, supporting separate agent choices for authoring and implementation, exposing the agent and model used for active story implementation, exposing workflow and issue-publishing options during PRD creation, enabling multiline terminal input, and keeping implementation controls synchronized with the actual implementation session.

Today, switching away from a New PRD tab destroys or disconnects its terminal, common PRD actions require indirect navigation, Edit PRD sessions are mislabeled as New PRD, and important workflow choices cannot be made when a PRD is created. An initial implementation-start error can also leave an error dialog visible after a retry has successfully started implementation, while Pause and Stop remain disabled. These gaps interrupt users, can cause lost conversational context or duplicated work, and can prevent users from safely controlling an active implementation. The changes in this PRD should make PRD creation and implementation-session control feel persistent, predictable, and consistent with the process that is actually running.

For this PRD, a **PRD authoring session** is the live conversational agent process and terminal state used to create or edit a PRD. An **implementation agent** is the agent selected to execute the PRD's user stories after the user explicitly starts implementation.

## 2. Goals

- Preserve 100% of an active PRD authoring session's process, terminal history, and draft input when users switch application tabs.
- Make New PRD available from the File menu and keyboard in one action.
- Organize the PRD sidebar with New PRD above a visual divider and existing PRDs below it.
- Make Edit PRD, Open markdown file, and Delete PRD available from both a three-dot menu on each PRD and its right-click context menu.
- Clearly label conversational editing tabs as `Edit PRD`, never `New PRD`.
- Provide sidebar shortcuts that open the current repository on GitHub, in VS Code, or in a selected supported AI IDE.
- Detect whether VS Code, Claude, Cursor, and Codex are installed before launch and show an actionable alert instead of failing silently when a selected application is unavailable.
- Show the actual implementation agent and model on every user story while that story is being implemented.
- Let users independently choose authoring and implementation agents, with both selectors initialized from the applicable configured defaults.
- Let users configure stacked pull requests and optional Linear or GitHub issue publishing before PRD generation.
- When issue publishing is enabled, create one issue per generated user story and write the resulting issue identifier and link back into that story in the saved PRD.
- Allow multiline terminal prompts using `Shift+Enter` while preserving `Enter` as the submit action.
- Keep implementation status, dialogs, and Start, Pause, Resume, and Stop controls synchronized with the authoritative implementation-session state, including after failed starts and retries.
- Ensure repeated Start, Pause, Resume, and Stop commands are safe and do not create duplicate sessions or leave an active session without usable controls.

## 3. User Stories

### US-001: Preserve PRD authoring sessions across tab changes
**Status:** done
**Priority:** 1
**Description:** As a user, I want an active PRD authoring session to remain alive when I switch tabs so that I can return without losing the conversation or terminal state.

**Acceptance Criteria:**
- [x] Switching from a New PRD or Edit PRD tab to any other application tab does not terminate, restart, or replace the authoring agent process.
- [x] Returning to the authoring tab shows the same terminal output, scrollback, session status, and unsent input that were present before the tab change.
- [x] Output produced while the authoring tab is inactive appears in the same session when the user returns.
- [x] A completed, failed, or canceled session retains its final terminal output when the user changes tabs and returns.
- [x] Opening or selecting another PRD does not attach its terminal to the preserved session.
- [x] Closing the authoring tab uses the application's existing close-session behavior and releases the session resources; ordinary tab switching does not.
- [x] Automated tests cover repeated tab switches during active, completed, failed, and canceled sessions.

### US-002: Support multiline terminal input
**Status:** done
**Priority:** 2
**Description:** As a user, I want `Shift+Enter` to add a line break to my terminal prompt so that I can compose structured instructions before submitting them.

**Acceptance Criteria:**
- [x] Pressing `Shift+Enter` inserts a newline at the current cursor position and does not submit the input.
- [x] Pressing `Enter` without Shift submits the complete input, including any inserted line breaks.
- [x] Multiline input remains intact when the user switches tabs and returns.
- [x] `Shift+Enter` works in both New PRD and Edit PRD authoring terminals.
- [x] Terminal input tests distinguish `Enter`, `Shift+Enter`, and the platform's input-method composition events so composition is not submitted prematurely.

### US-003: Add New PRD to the File menu
**Status:** done
**Priority:** 3
**Description:** As a user, I want to create a PRD from the File menu or a keyboard shortcut so that I can begin authoring without navigating through the sidebar.

**Acceptance Criteria:**
- [x] The application File menu contains a `New PRD` item.
- [x] The menu displays `⌘N` on macOS and the platform-appropriate `Ctrl+N` equivalent on Windows and Linux.
- [x] Selecting the menu item or pressing its shortcut opens and focuses a New PRD tab.
- [x] The action has the same result as the existing New PRD entry point and does not create a duplicate session when a single command event is handled more than once.
- [x] The shortcut is disabled or ignored when an application modal requiring a response is open.
- [x] Menu and shortcut behavior is covered by automated tests on supported desktop platforms.

### US-004: Add PRD sidebar action menus
**Status:** done
**Priority:** 4
**Description:** As a user, I want a clear PRD sidebar and discoverable actions for each PRD so that I can create or manage PRDs without indirect navigation.

**Acceptance Criteria:**
- [x] The PRD sidebar displays `New PRD` before the list of existing PRDs.
- [x] A visible line separator or styled divider separates `New PRD` from the existing PRD list.
- [x] Each existing PRD row has a three-dot overflow button that appears when the row is hovered or receives keyboard focus.
- [x] On touch-only devices, the three-dot button remains visible without requiring hover.
- [x] Activating the three-dot button opens a dropdown anchored to that button with `Edit PRD`, `Open markdown file`, and `Delete PRD`, in that order.
- [x] Right-clicking a PRD row opens a context menu with the same three actions in the same order.
- [x] Either menu operates on the PRD whose row or three-dot button opened it, even when a different PRD was previously selected.
- [x] Opening one PRD action menu closes any other open PRD action menu.
- [x] An open menu dismisses on outside click, `Escape`, focus loss, or completion of an action.
- [x] The three-dot button has an accessible name that identifies its PRD, and the button and every menu action are operable using only the keyboard.
- [x] Automated tests verify sidebar ordering, divider presence, hover and focus visibility, touch visibility, target selection, action parity between both menus, dismissal, and keyboard access.

### US-005: Edit a PRD through an authoring session
**Status:** done
**Priority:** 5
**Description:** As a user, I want Edit PRD to open a conversational editing session so that I can revise an existing PRD with an agent.

**Acceptance Criteria:**
- [x] Selecting `Edit PRD` opens a dedicated authoring tab for the targeted PRD and starts or restores an editing session with the current PRD content available as context.
- [x] The editing tab is titled `Edit PRD` and is never titled `New PRD`.
- [x] The tab also identifies the targeted PRD within the tab content so two Edit PRD sessions can be distinguished.
- [x] The editing session uses the selected authoring agent for that session, defaulting to the configured authoring agent when no session-specific choice exists.
- [x] Changes are written only after the conversational workflow reaches its existing explicit save/confirmation point; merely opening Edit PRD does not modify the file.
- [x] Switching tabs preserves the editing session according to US-001.
- [x] If the PRD file is missing or unreadable, the application displays an actionable error and does not start an empty replacement PRD.

### US-006: Delete or open a PRD markdown file from its action menu
**Status:** done
**Priority:** 6
**Description:** As a user, I want to delete a PRD safely or open its markdown file in my default editor so that I can manage the PRD directly from the sidebar.

**Acceptance Criteria:**
- [x] Selecting `Delete PRD` opens a confirmation dialog naming the targeted PRD before any file is removed.
- [x] Canceling the dialog leaves the PRD and its files unchanged.
- [x] Confirming deletion uses the application's existing PRD deletion semantics and removes the PRD from the sidebar only after deletion succeeds.
- [x] Deletion is blocked with a clear explanation while that PRD has an active authoring or implementation session; the application does not silently terminate either session.
- [x] A deletion failure displays an actionable error and leaves or restores the sidebar entry.
- [x] Selecting `Open markdown file` asks the operating system to open the targeted PRD `.md` file with the default application associated with markdown files.
- [x] `Open markdown file` does not start an Edit PRD authoring session or change the active tab in Loop.
- [x] If the operating system has no application associated with markdown files, the application displays an actionable error that identifies the targeted file.
- [x] A missing or unreadable markdown file produces an actionable error and does not open another file or directory.

### US-007: Select authoring and implementation agents
**Status:** done
**Priority:** 7
**Description:** As a user, I want separate agent selectors for PRD authoring and implementation so that I can use the best agent for each phase.

**Acceptance Criteria:**
- [x] The New PRD tab shows separate, clearly labeled `Authoring agent` and `Implementation agent` selectors before generation starts.
- [x] Each selector lists only installed and available agents supported for that phase.
- [x] The authoring selector defaults to the configured default authoring agent, and the implementation selector defaults to the configured default implementation agent; if only one general default exists, both initially use it.
- [x] Changing one selector does not change the other.
- [x] The chosen authoring agent is used when the PRD authoring session starts.
- [x] The chosen implementation agent is saved with the PRD and is preselected when implementation is started.
- [x] The implementation-start UI allows the user to change the implementation agent before confirming the run.
- [x] If a saved agent is no longer available, the UI reports that condition and requires a valid replacement before the affected phase can start.

### US-008: Configure PR and issue-publishing options
**Status:** done
**Priority:** 8
**Description:** As a user, I want to choose a pull-request strategy and optional issue destination while creating a PRD so that its implementation workflow and external tracking are prepared from the start.

**Acceptance Criteria:**
- [x] The New PRD tab includes a `Stack PR per user story` option that is off by default unless a saved application preference explicitly supplies another default.
- [x] The New PRD tab includes an issue-publishing choice with `Do not publish`, `Linear`, and `GitHub Issues` options.
- [x] Only destinations configured and authenticated for the current project are enabled; unavailable destinations explain what configuration is missing.
- [x] The chosen PR strategy and issue destination are visible for review before PRD generation begins.
- [x] The chosen PR strategy is saved as PRD workflow metadata and is available to the later implementation workflow; selecting it does not start implementation or create pull requests.
- [x] Selecting `Do not publish` causes PRD generation to perform no Linear or GitHub write operations.
- [x] Configuration and validation tests cover each option and missing integration credentials.

### US-009: Publish generated user stories as issues and record references
**Status:** done
**Priority:** 9
**Description:** As a user, I want generated user stories published to my selected tracker and linked back to the PRD so that the PRD and external work items remain traceable.

**Acceptance Criteria:**
- [x] When Linear or GitHub Issues is selected, publishing begins only after the generated PRD has been successfully saved.
- [x] The system creates exactly one issue for each generated user story, using the story title and description and including its acceptance criteria.
- [x] Successfully created issues are not duplicated when publishing is retried after a partial failure; the system uses stored external references or an equivalent idempotency mechanism.
- [x] After each issue is created, the saved PRD user story records the destination, human-readable issue identifier, and clickable issue URL in a consistent `External Issue` metadata line.
- [x] The issue reference is attached to the matching user story and is not written to another story when responses complete out of order.
- [x] PRD file updates are atomic so an interrupted write does not corrupt or truncate the generated PRD.
- [x] A partial publishing failure reports which stories succeeded and failed, preserves references for successful stories, and offers a retry for failed stories only.
- [x] Closing or switching tabs while publishing is in progress does not cancel publishing; current progress is visible when the user returns.
- [x] Integration tests cover Linear, GitHub Issues, no-publish mode, partial failure, retry, duplicate prevention, and PRD reference persistence.

### US-010: Document and verify the improved workflows
**Status:** done
**Priority:** 10
**Description:** As a maintainer, I want workflow documentation and end-to-end coverage so that these UI improvements remain reliable across releases and platforms.

**Acceptance Criteria:**
- [x] User documentation describes session persistence, `Shift+Enter`, File > New PRD, the PRD sidebar layout, repository launchers, three-dot and right-click actions, Edit PRD tab naming, both agent selectors, active-story agent and model indicators, stacked PRs, and issue publishing.
- [x] Documentation explains that issue publishing occurs after generation and that external issue references are written back into the PRD.
- [x] An end-to-end test creates a PRD with distinct authoring and implementation agents, switches tabs during authoring, uses multiline input, and verifies the preserved session.
- [x] An end-to-end test publishes multiple stories to a test tracker, verifies one external issue per story, and verifies the identifier and URL stored in each PRD story.
- [x] Platform-specific tests or test doubles cover native menu shortcuts, PRD action menus, opening markdown files with the operating system's default application, detecting supported local editors, and launching the repository with the correct path.
- [x] The project's formatting, type checking, unit tests, integration tests, and end-to-end tests pass.

### US-011: Synchronize implementation controls with session state
**Status:** done
**Priority:** 1
**Description:** As a user, I want implementation status and controls to reflect the actual implementation session so that I can safely retry a failed start and pause, resume, or stop any session that is running.

**Acceptance Criteria:**
- [x] The UI derives the displayed implementation state and enabled controls from the authoritative implementation-session state rather than from the result of the most recent Start request alone.
- [x] While a Start request is unresolved, the UI shows a `Starting` state and prevents additional Start requests for the same PRD.
- [x] If a Start request fails and no implementation session exists, the UI shows an actionable error, returns the PRD to a startable state, and keeps Pause and Stop disabled.
- [x] After a failed Start request, selecting Start again creates at most one implementation session for that PRD.
- [x] When a retry successfully starts implementation, any stale start-error dialog for that PRD is dismissed or replaced by the current running state.
- [x] When the authoritative session state is `Running`, Start is disabled and Pause and Stop are enabled, including when the session started after an earlier error.
- [x] When the authoritative session state is `Paused`, Resume and Stop are enabled and Start and Pause are disabled.
- [x] While a Pause, Resume, or Stop request is unresolved, the UI displays the corresponding transition and prevents duplicate requests for that action.
- [x] When stopping completes, the UI shows a non-running terminal state and disables Pause and Stop; the PRD can subsequently be started again according to the existing restart rules.
- [x] If a Pause, Resume, or Stop request fails, the UI reconciles with the authoritative session state, displays an actionable error, and enables the controls valid for that reconciled state.
- [x] Reopening the PRD or switching away and back reconstructs the same implementation state and valid controls without starting, pausing, resuming, or stopping a session.
- [x] Automated tests cover an initial Start failure followed by a successful retry, stale error-dialog cleanup, duplicate Start prevention, control availability for every session state, and failures during Pause, Resume, and Stop.

### US-012: Open the repository in GitHub or a local editor
**Status:** done
**Priority:** 12
**Description:** As a user, I want repository-launch shortcuts in the PRD sidebar so that I can quickly open the current project on GitHub, in VS Code, or in a supported AI IDE.

**Acceptance Criteria:**
- [x] A repository-launcher group appears at the top of the PRD sidebar near `New PRD` without interrupting the visual separation between New PRD and the existing PRD list.
- [x] The group contains a GitHub icon button, a VS Code icon button, and an AI IDE icon button.
- [x] Every icon button has a visible tooltip and an accessible name that describes its action; repository launchers are operable using only the keyboard.
- [x] Selecting the GitHub button opens the current repository's configured GitHub remote in the user's default web browser.
- [x] The GitHub remote is converted to a valid HTTPS repository page whether the configured remote uses an HTTPS or SSH Git URL.
- [x] If the project has no GitHub remote, has a malformed remote, or uses a non-GitHub host, the application displays an actionable alert explaining that no GitHub repository is configured and does not open an unrelated URL.
- [x] Selecting the VS Code button first checks whether VS Code is installed and, when available, opens the current repository root in VS Code.
- [x] Selecting the AI IDE button opens a dropdown containing `Claude`, `Cursor`, and `Codex`, regardless of which applications are installed.
- [x] Selecting Claude, Cursor, or Codex first checks whether that specific application is installed and, when available, opens the current repository root in that application.
- [x] If VS Code or the selected AI IDE is not installed, the application displays an alert naming the unavailable application and explaining that it must be installed before the repository can be opened there.
- [x] Dismissing an unavailable-application alert leaves Loop and the current repository state unchanged.
- [x] If an application is detected but launching it fails, the application displays an actionable launch-failure alert that is distinguishable from the not-installed alert.
- [x] Activating a launcher once results in at most one browser tab or application-launch request.
- [x] Automated tests cover GitHub HTTPS and SSH remotes, missing and non-GitHub remotes, installed and unavailable applications, launch failures, duplicate-event prevention, dropdown keyboard navigation, and correct repository-path forwarding.

### US-013: Display the active story's implementation agent and model
**Status:** done
**Priority:** 13
**Description:** As a user, I want to see which agent and model are implementing an in-progress user story so that I can verify the execution configuration without leaving the PRD view.

**Acceptance Criteria:**
- [x] When a user story enters an active implementation state, its visible story row or card displays an `Agent` value and a `Model` value.
- [x] The displayed values identify the agent and model actually assigned to the active implementation session, not merely the current application defaults or the values currently selected elsewhere in the UI.
- [x] Agent and model metadata remains visible while the story is starting, running, pausing, paused, resuming, or stopping.
- [x] If the implementation session exists but either value has not yet been resolved, the corresponding field displays `Resolving…` and does not show a potentially incorrect default.
- [x] If the session reports that an agent or model value is unavailable, the corresponding field displays `Unavailable` while preserving the other value when known.
- [x] A successful retry or restarted implementation updates the story to show the agent and model assigned to the new active session; delayed metadata from an earlier session cannot overwrite it.
- [x] Switching tabs, reopening the PRD, or remounting the story view restores the agent and model for any active implementation session.
- [x] When a story no longer has an active implementation session, the active Agent and Model indicator is removed from that story; this requirement does not add historical execution metadata.
- [x] Agent and model values are exposed as readable text, are available to assistive technology, and do not rely on icons, color, or tooltips alone.
- [x] Automated tests cover resolved, resolving, partially unavailable, paused, retried, restarted, remounted, and inactive story states, including protection from stale session metadata.

## 4. Functional Requirements

- **FR-1:** The system must keep every open PRD authoring process mounted or otherwise retained independently of the currently visible application tab.
- **FR-2:** Tab visibility changes must not start, stop, restart, or reassign an authoring process.
- **FR-3:** A retained authoring session must preserve terminal output, scrollback, status, and unsent input, including multiline input.
- **FR-4:** Output received while an authoring tab is inactive must be delivered to that session and displayed when the tab becomes active.
- **FR-5:** `Shift+Enter` must insert a newline without submission in New PRD and Edit PRD terminals; unmodified `Enter` must submit the full input.
- **FR-6:** The File menu must expose New PRD with `⌘N` on macOS and the platform-equivalent `Ctrl+N` shortcut elsewhere.
- **FR-7:** File > New PRD and its shortcut must invoke the same New PRD action as the existing UI entry point.
- **FR-8:** The sidebar must display New PRD above a visual divider, followed by the existing PRD list.
- **FR-9:** Each existing PRD must expose a three-dot overflow button on pointer hover and keyboard focus, and must keep that button visible on touch-only devices.
- **FR-10:** Delete PRD must require confirmation that names the target and must not silently stop an active session.
- **FR-11:** The three-dot dropdown and right-click context menu must provide the same ordered actions for the targeted PRD: Edit PRD, Open markdown file, and Delete PRD.
- **FR-12:** The New PRD UI must provide independent Authoring agent and Implementation agent selectors.
- **FR-13:** Agent selectors must initialize from phase-specific configured defaults, falling back to the general configured default when phase-specific defaults do not exist.
- **FR-14:** The system must validate agent availability before starting authoring or implementation and must require replacement of an unavailable saved agent.
- **FR-15:** The selected implementation agent must be stored with the PRD and remain changeable at the explicit implementation-start confirmation step.
- **FR-16:** The New PRD UI must provide a `Stack PR per user story` option and an issue destination choice of none, Linear, or GitHub Issues.
- **FR-17:** Selecting stacked PRs must store workflow metadata for later implementation and must not itself create branches, commits, or pull requests.
- **FR-18:** Issue publishing must begin after, and only after, the generated PRD is successfully saved.
- **FR-19:** When publishing is enabled, the system must create one external issue for each generated user story using its title, description, and acceptance criteria.
- **FR-20:** Each successfully published story must contain an `External Issue` metadata line with the tracker name, issue identifier, and issue URL.
- **FR-21:** External issue references must be mapped by stable user-story identity rather than response order.
- **FR-22:** Issue creation and retry behavior must be idempotent and must not duplicate issues that have already been recorded as successfully created.
- **FR-23:** A partial publishing failure must preserve successful references, identify failed stories, and permit retrying only unresolved stories.
- **FR-24:** Switching tabs must not cancel issue publishing, and returning to the PRD tab must show current or final publishing status.
- **FR-25:** The application must disable unconfigured issue destinations and present actionable setup guidance without exposing integration credentials.
- **FR-26:** Context-menu actions, menu commands, repository launchers, agent selectors, creation options, terminal input, and publishing status must support keyboard operation and accessible labels.
- **FR-27:** The application must maintain one authoritative implementation-session state per PRD and use it to determine the displayed status and enabled Start, Pause, Resume, and Stop controls.
- **FR-28:** Starting an implementation must be idempotent for a PRD while its session is starting, running, paused, pausing, resuming, or stopping; repeated commands must not create another concurrent implementation session.
- **FR-29:** A failed Start request must not be treated as proof that no session exists. Before presenting the PRD as startable, the application must reconcile the request result with the authoritative session state.
- **FR-30:** When a Start retry succeeds, the application must clear any stale start-error presentation associated with the earlier failed attempt.
- **FR-31:** Pause, Resume, and Stop availability must be based on the reconciled session state, including when the session becomes active after an earlier Start error.
- **FR-32:** While a session-control command is in progress, the application must expose its transition state and prevent duplicate commands that could conflict with that transition.
- **FR-33:** After any Start, Pause, Resume, or Stop command succeeds or fails, the application must reconcile with the authoritative session state and render only the controls valid for that state.
- **FR-34:** Tab changes, PRD reselection, and view remounting must restore implementation status and controls from session state without issuing a new session-control command.
- **FR-35:** Edit PRD must open a conversational authoring session with the current PRD content as context, title its tab `Edit PRD`, and not modify the file merely by opening it.
- **FR-36:** Open markdown file must invoke the operating system's default application for the targeted PRD `.md` file without starting a Loop authoring session.
- **FR-37:** Every sidebar action must remain bound to the PRD that opened its menu and must never act on a different selected or previously active PRD.
- **FR-38:** The top of the PRD sidebar must provide icon buttons for GitHub, VS Code, and an AI IDE dropdown near the New PRD action.
- **FR-39:** The AI IDE dropdown must always list Claude, Cursor, and Codex and must not hide an entry merely because the corresponding application is unavailable.
- **FR-40:** The GitHub launcher must resolve the current repository's configured GitHub remote, normalize supported HTTPS and SSH Git remote formats to an HTTPS repository page, and open that page in the default browser.
- **FR-41:** When no valid GitHub remote is configured, the GitHub launcher must display an actionable alert and must not attempt to open a fallback URL or local directory.
- **FR-42:** Before requesting a local editor launch, the application must check the availability of the selected VS Code, Claude, Cursor, or Codex integration using platform-appropriate application identifiers or executables.
- **FR-43:** When the selected local application is available, the launcher must open the current repository root in that application and must pass the path as a discrete launch argument rather than interpolated shell text.
- **FR-44:** When the selected local application is unavailable, the launcher must display an alert naming the application and explaining that installation is required; the application entry must remain visible in the UI.
- **FR-45:** A launch failure after successful application detection must produce an actionable error distinct from an unavailable-application alert.
- **FR-46:** Repository launcher commands must be idempotent per user activation so overlapping UI and native events do not produce duplicate browser tabs or application launches.
- **FR-47:** Each user story with an active implementation session must display the actual agent and model assigned to that session.
- **FR-48:** Agent and model metadata must be keyed by both stable user-story identity and implementation-session identity and must not be inferred solely from configured defaults.
- **FR-49:** The active Agent and Model indicator must remain visible through starting, running, pausing, paused, resuming, and stopping states and must be removed when no active implementation session remains.
- **FR-50:** Unresolved metadata must display `Resolving…`; explicitly unavailable metadata must display `Unavailable` without hiding a known value from the other field.
- **FR-51:** Session retries, restarts, tab changes, and view remounting must reconcile the displayed agent and model with the authoritative active implementation session.

## 5. Non-Goals (Out of Scope)

- Starting PRD implementation automatically after PRD generation or issue publishing.
- Creating branches, commits, or pull requests during PRD authoring; the stacked-PR option only configures later implementation behavior.
- Synchronizing later edits bidirectionally between PRD user stories and existing Linear or GitHub issues.
- Publishing the same generation to Linear and GitHub simultaneously in the first release; one destination may be selected per PRD generation.
- Importing Linear or GitHub issues into a PRD.
- Adding new agent providers, installing missing agent CLIs, or managing provider credentials from the agent selectors.
- Preserving authoring sessions after the application process exits or the project is closed; this release covers navigation among tabs within the open project session.
- Changing the existing semantics for permanently closing or explicitly canceling an authoring session.
- Redesigning the entire sidebar, tab system, terminal, File menu, or implementation workflow beyond the PRD grouping, divider, menus, controls, and behavior specified here.
- Deleting external tracker issues when a local PRD or user story is deleted.
- Automatically retrying failed implementation starts or session-control commands without an explicit user action.
- Allowing more than one concurrent implementation session for the same PRD.
- Installing VS Code, Claude, Cursor, or Codex, or modifying the user's default browser or file associations.
- Supporting repository hosts other than GitHub in the initial repository web launcher.
- Automatically choosing or persisting a default AI IDE; the user selects Claude, Cursor, or Codex from the dropdown for each launch.
- Adding a historical audit log of agents and models used by completed, failed, canceled, or stopped story implementations.

## 6. Design Considerations

- Keep New PRD options grouped by phase: authoring agent, implementation agent, implementation workflow, and issue publishing. The distinction between generating a PRD and implementing it should remain visually explicit.
- Label the authoring selector and implementation selector with helper text explaining when each agent is used.
- Show the resolved default in each agent selector; do not use an ambiguous blank value to mean default.
- Disable unavailable tracker destinations in place and explain the required project configuration or authentication near the control.
- Display issue-publishing progress by story, including pending, created, and failed states. Preserve the generated PRD as a successful result even if external publishing fails.
- Use native desktop conventions for File > New PRD, shortcut glyphs, context menus, confirmation dialogs, and opening files with their default applications.
- Destructive actions must be visually separated from non-destructive context-menu actions, with Delete PRD placed last.
- Match the interaction hierarchy of Claude's sidebar without requiring a pixel-for-pixel copy: a primary New PRD action, a restrained divider, a readable list of existing PRDs, and low-visual-weight three-dot controls that appear when needed.
- The divider must be visually clear in supported themes without being mistaken for an interactive control.
- A hovered, keyboard-focused, or right-clicked PRD may receive a temporary target highlight without unexpectedly navigating away from the currently open tab.
- The three-dot button must remain visible while its dropdown is open, even if the pointer leaves the PRD row.
- Place the repository-launcher icons near New PRD as a compact, visually related group while keeping New PRD as the primary sidebar action.
- Use recognizable GitHub and VS Code icons. Use a neutral AI/editor icon for the AI IDE dropdown so the control does not imply that one of Claude, Cursor, or Codex is preselected.
- Tooltips must use action-oriented labels: `Open GitHub repository`, `Open in VS Code`, and `Open in AI IDE`.
- The AI IDE dropdown must show text labels for Claude, Cursor, and Codex. Installation status may be shown as secondary text, but unavailable entries remain selectable so the required alert can be displayed.
- Unavailable-application alerts must name the application and explain the next step. Launch-failure alerts must state that the application was found but could not open the repository.
- Place the active Agent and Model indicator within the in-progress story row or card near its implementation status, without displacing the story title or primary session controls.
- Use compact text labels such as `Agent: Codex` and `Model: GPT-5`. Long values must truncate visually without losing the complete accessible text or tooltip value.
- `Resolving…` and `Unavailable` must be visually distinguishable from resolved values and must not make the story appear failed by themselves.
- For saved PRD markdown, use this stable story metadata format directly below the story description and before acceptance criteria: `**External Issue:** [TRACKER-ID](https://tracker.example/issue)`.
- Present implementation states with distinct, accessible status text such as `Starting`, `Running`, `Pausing`, `Paused`, `Resuming`, `Stopping`, `Stopped`, and `Failed`; do not rely on button availability or color alone to communicate state.
- Error dialogs must identify the failed action. They must not remain as the primary UI state after reconciliation shows that the implementation is running or paused.
- During transitional states, disable only conflicting controls and show progress on the action being processed so users can distinguish a pending command from an unresponsive control.

## 7. Technical Considerations

- Session ownership must live above tab-view mounting so hiding or unmounting presentation components cannot destroy the underlying authoring process. Each terminal must be keyed by a stable session ID and PRD identity.
- Terminal output may continue while a tab is inactive. Buffering must be bounded or use the terminal's existing scrollback limit so long-running hidden sessions do not grow memory without limit.
- Draft terminal input is UI state and must remain separate for each session; switching between two authoring tabs must never exchange their drafts.
- Native File menu commands and renderer shortcuts can emit overlapping events on some platforms. Route both through one command handler and prevent duplicate session creation.
- Opening a markdown file must use the desktop runtime's supported open-file API with a validated absolute path. User-controlled path text must not be passed through an interpolated shell command.
- Persist the implementation-agent selection, stacked-PR setting, issue destination, and per-story external reference in a backward-compatible PRD representation. Existing PRDs without this metadata must continue to open.
- Tracker integrations must use the project's existing Linear and GitHub authentication and repository/team configuration. Credentials and access tokens must never be written into PRD files, terminal output, or logs.
- Use a stable internal user-story ID such as `US-001` as the correlation key across generation, issue creation, response handling, retry, and markdown update.
- Write issue references atomically. If the application stops after issue creation but before the PRD update, recovery must reconcile created issues using stored operation state or a deterministic marker before attempting another create.
- Tracker API calls may complete out of order or be rate-limited. Publishing should expose per-story status and retry transient failures with bounded backoff while preserving deterministic story mapping.
- Editing an existing PRD may change story identifiers. Handling synchronization or migration of previously linked external issues is outside scope; the editor must preserve existing external-reference metadata unless the user explicitly changes the associated story content through the established edit workflow.
- Treat the implementation process or session manager as the source of truth. Request-level errors, dialogs, and local component flags are transient UI state and must not override a newer authoritative session state.
- Associate every session-control request and response with both the PRD identity and implementation-session identity so a delayed response from an earlier Start attempt cannot overwrite the state of a newer successful attempt.
- Serialize conflicting session-control commands per PRD and make command handling idempotent. A duplicate Start or Stop event must resolve to the existing/current session outcome rather than creating a second process or failing into an inconsistent UI state.
- Reconcile state after transport errors and timeouts because a command may have reached the session manager even when its response was not received by the UI.
- Session-state subscriptions and initial state queries must converge on the same state model so remounting a view cannot temporarily re-enable Start or disable Pause and Stop for an active session.
- Resolve the repository root from trusted project state rather than the currently selected PRD path. All local editor launchers must receive the same normalized repository-root path.
- Resolve the GitHub web URL from the project's configured Git remote without embedding credentials, tokens, or user information in the opened URL or application logs.
- Maintain a platform-specific registry of stable application identifiers and supported executable names for VS Code, Claude, Cursor, and Codex. Availability detection and launch behavior must use the same resolved installation to avoid reporting an application as installed and launching a different binary.
- Application detection must not execute untrusted repository content. Launch paths and repository paths must be passed through structured desktop-runtime APIs or escaped argument arrays, never an interpolated shell command.
- Source active agent and model values from the authoritative implementation-session metadata. Do not reconstruct model identity from provider defaults because a session may use an override or a provider-selected fallback.
- Correlate metadata updates with both the stable story ID and session ID. Ignore late metadata events belonging to a superseded, stopped, or failed session.
- Agent and model display values must exclude credentials, command arguments, system prompts, endpoint URLs containing secrets, and other private provider configuration.

## 8. Success Metrics

- In end-to-end tests, an active authoring session survives at least 20 consecutive tab changes with the same process identity, complete terminal output, and unchanged draft input.
- In automated tests, 100% of output produced while an authoring tab is inactive appears in the correct terminal after returning, with no cross-session output.
- Users can open New PRD from anywhere in the main application with one menu selection or one keyboard shortcut.
- Users can initiate Edit PRD, Open markdown file, or Delete PRD for a sidebar PRD in no more than two interactions.
- In UI tests, 100% of Edit PRD sessions display `Edit PRD` as the tab title and never display `New PRD`.
- `Shift+Enter` inserts a newline without causing a submission in 100% of terminal input tests, while `Enter` submits the multiline value once.
- In integration tests, every successfully generated user story produces exactly one issue when publishing is enabled, and 100% of created issue IDs and URLs are recorded under the matching PRD story.
- Retrying a simulated partial publish creates zero duplicates and retries only unresolved stories.
- A tracker outage never removes or corrupts the successfully generated local PRD in integration tests.
- All new commands and controls pass the project's keyboard-navigation and accessible-name checks.
- In automated race-condition tests, an initial Start error followed by a successful retry results in exactly one running implementation session, no stale start-error dialog, Start disabled, and Pause and Stop enabled.
- In state-transition tests, 100% of Start, Pause, Resume, and Stop outcomes render the controls defined for the authoritative session state after reconciliation.
- Repeated Start and Stop events create zero duplicate implementation sessions and leave no running session without an enabled Stop control.
- After simulated command timeouts and delayed responses, the UI converges to the authoritative implementation state without requiring an application restart or project reload.
- In launcher tests, valid GitHub HTTPS and SSH remotes open the correct repository page in exactly one browser request, while missing or non-GitHub remotes open zero URLs and show an actionable alert.
- In platform integration tests, each installed VS Code, Claude, Cursor, and Codex target receives the exact current repository-root path in one launch request.
- In unavailable-application tests, all supported launcher entries remain visible and 100% of selections show an alert naming the missing application without changing project state.
- In implementation-state tests, every active story displays the agent and model reported by its authoritative session, with zero substitutions from current defaults.
- In retry and remount tests, stale session metadata produces zero incorrect agent or model updates and the active values are restored without restarting implementation.

## 9. Open Questions

- When a repository has multiple GitHub remotes, should the launcher always prefer `origin`, prefer the application's configured primary remote, or ask the user which remote to open?
- For Claude and Codex, which installed desktop application, IDE integration, or CLI executable should qualify as the supported launch target on each operating system?
- Where should the new phase-specific authoring and implementation agent defaults be configured if the current application exposes only one general agent default?
- What exact project/team and repository/label defaults should Linear and GitHub issue publishing use when more than one valid destination is configured?
- Should issue descriptions include only the individual story or also repeat PRD-level context such as goals, non-goals, and technical considerations?
- Should the application present a notification badge on an inactive PRD tab when its authoring session completes or needs user input?
- What existing close-session behavior should be used when a user closes a tab whose authoring agent is still running: confirmation, background continuation, or explicit cancellation?
