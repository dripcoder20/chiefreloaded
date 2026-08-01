# Parity with the chief TUI

Keyed to the chief source file each feature came from, so a reviewer can diff
this against upstream rather than trust a summary. Update it in the same commit
as the feature.

Legend: **done** · **partial** · **todo** · **n/a** (deliberately not ported)

## Engine

| Feature | chief source | State | Notes |
|---|---|---|---|
| Story loop | `internal/loop/loop.go` | done | Vendored. Driven one story at a time via `RunStory`. |
| Agent providers | `internal/agent/*` | done | Vendored: claude, codex, opencode, cursor. Gemini landed upstream after v0.8.0. |
| PRD parse/write | `internal/prd/*` | done | Vendored, tests included. |
| Crash retry + watchdog | `internal/loop/loop.go` | done | Vendored behaviour, reused unchanged. |
| Attempt budget | `app.go` (inline) | done | `max(5, incomplete+5)`, now in `session/run.go` and unit-tested. |
| In-progress cleanup | `app.go:2176` | done | Plus on `OpenProject` — chief never clears a crashed run's wedged story. |
| Commit verification | — | done | New. `headBefore..HEAD` range check; chief's grep matches ancestors. |
| Process-group kill | — | done | New. chief's Stop can hang when the agent has spawned children. |

## Runs

| Feature | chief source | State | Notes |
|---|---|---|---|
| Start / pause / stop | `app.go` | done | |
| Adjust max iterations | `app.go` (`+`/`-`) | done | Called the attempt budget here. |
| Parallel PRDs | `internal/loop/manager.go` | partial | The session runs several; the UI shows one at a time with live state in the rail. |
| Per-story timings | `app.go:1238` | partial | Recorded; no summary screen yet. |

## Git

| Feature | chief source | State | Notes |
|---|---|---|---|
| Branch safety decision | `app.go:726`, `1092` | done | Now data (`Question`), not keystroke handling. Unit-tested. |
| Worktree provisioning | `app.go:1627` | done | Output streamed and genuinely cancellable; chief's `CombinedOutput` is neither. |
| Per-PRD push / PR | `app.go:1310` | todo | Config keys exist and migrate; the action is not wired. |
| **Per-story stacked PRs** | — | done | New. `gh stack` with a manual `gh pr create --draft --base` fallback. |
| Merge branch | `picker.go` | todo | |
| Clean worktree | `picker.go` | todo | |
| Orphaned worktree detection | `git.DetectOrphanedWorktrees` | todo | |

## Interface

| Feature | chief source | State | Notes |
|---|---|---|---|
| Story list + status icons | `dashboard.go:369` | done | |
| Progress bars | `dashboard.go:666` | done | In the rail. |
| Story detail / criteria | `dashboard.go:451` | done | Plus the "auto-ticked, not verified" caveat chief lacks. |
| PRD picker | `picker.go` | partial | A permanent rail rather than a modal. Switch works; create/delete do not. |
| Tab bar | `tabbar.go` | n/a | The rail is the same list; rendering it twice was a terminal workaround. |
| Log view | `log.go` | partial | Streams with colour-coded rows and auto-scroll. No syntax highlighting, no search, not yet virtualised. |
| Diff view | `diff.go` | todo | |
| Settings | `settings.go` | done | Exceeds chief: exposes the agent provider and the git mode it has no field for. |
| Help overlay | `help.go` | partial | Key hints in the status bar; no palette or overlay. |
| Branch-safety dialog | `branch_warning.go` | done | Rendered from the `Question` the session emits. |
| Worktree progress | `worktree_spinner.go` | partial | Steps are emitted; no dedicated progress UI. |
| Completion screen | `completion.go` | todo | Including the confetti, which should be one short burst rather than continuous. |
| Quit confirmation | `quit_confirm.go` | todo | |
| First-run wizard | `first_time_setup.go` | todo | An empty state points at `chief new`. |
| PRD authoring (`new`/`edit`) | `cmd/new.go`, `edit.go` | todo | Needs the embedded PTY pane. |
| Keyboard map | `app.go` | partial | `s p x t , + - j k` work. No `1-9`, `d`, `l`, `n`, `e`, `?`, `g/G`. |

## Deliberate departures

- **The PRD list is navigation, not a modal.** chief hides it behind `l` while
  filling it with live state you are meant to watch. Those two facts contradict
  each other in a terminal; here they do not have to.
- **A completed PRD will not take over the screen.** chief's completion view is
  a full-screen takeover in a system that runs several PRDs at once, so one
  finishing evicts your view of another mid-run.
- **Acceptance criteria on completed stories are labelled unreliable.**
  `SetStoryStatus(id, "done")` ticks every box as a side effect, so the checklist
  records the status write and nothing else.
- **No `prd.json` fallbacks.** `app.go` still references that path in four
  places though `LoadPRD` is markdown-only; those paths are simply broken.
