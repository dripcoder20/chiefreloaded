# Loop — a GUI for chief (chiefloop.com)

> **This is the original design document, written before any code existed.** It
> is the best explanation of *why* the architecture is shaped the way it is, and
> the findings in the table below are still load-bearing. It is **not** a
> description of the current state: milestones M0–M7 and M10 have shipped, and a
> few details were revised on contact with reality (notably `gh pr edit`, which
> does not work on gh 2.68 — see `internal/ghstack/ghstack.go`). For what is
> actually built, read [AGENTS.md](../AGENTS.md) and the code.
>
> **§3, and point 2 of the Context list below, are the largest revision.** Runs
> no longer push or open pull requests at all: a run only commits, and
> publishing — one pull request for the PRD, or one per story — is an action the
> user takes afterwards. Read them for why stacked branches are shaped the way
> they are, and
> [workflows.md](workflows.md#publishing) for what actually happens today.

## Context

[chief](https://github.com/MiniCodeMonkey/chief) (chiefloop.com, MIT, Go 1.24, v0.8.0) is a terminal TUI that builds software autonomously: it reads a markdown PRD, picks the next user story, spawns a coding-agent CLI (Claude Code / Codex / OpenCode / Cursor / Gemini) with a generated prompt, streams the agent's NDJSON output, and marks the story done when the agent emits the done sentinel. One commit per story.

We're building **Loop**, a Wails v3 desktop GUI in a new repo at `/Users/kristher/Code/DripCoder/loop` (currently empty), targeting **full TUI feature parity** plus two things the TUI can't do:

1. **Embedded PRD authoring** — `chief new`/`edit` currently quit the TUI and hand the terminal to an interactive agent session. In a GUI that becomes an in-app pane.
2. **Per-story stacked branches and draft PRs** — instead of one branch and one PR per PRD, each story gets its own branch stacked on the previous one, pushed with a draft PR the moment it completes, using [GitHub's stacked PRs](https://github.blog/changelog/2026-07-30-stacked-pull-requests-are-now-in-public-preview/) (public preview since 2026-07-30) via the `gh stack` extension.

**Decisions already made:** Wails v3 · separate repo, chief vendored · full parity · embedded chat pane · stacked on previous story · continue immediately after opening a PR · draft PRs.

---

## Findings that shape the design

Verified against the upstream source and locally-installed tooling.

| Finding | Consequence |
|---|---|
| The engine is UI-free — nothing outside `internal/tui` imports bubbletea. `loop.Loop.Events()` and `loop.Manager.Events()` are clean channels. | The engine is reusable as-is. |
| Only `chief/embed` is public; the whole engine is under `internal/` (~4,800 non-test LOC). Go's `internal/` rule is **directory-based**, so a separate module cannot import it — not even via `replace`. | Vendoring must copy source into our own tree with import rewriting. See §1. |
| Substantial orchestration *policy* lives in `internal/tui/app.go`, not the engine: dynamic max-iterations, branch-safety decision tree, worktree provisioning + setup command, auto push/PR, story timings, in-progress cleanup. | Must be re-homed into a headless `internal/session`. See §2. |
| `Loop.Run` calls `prd.SetStoryStatus(id, "done")` inline with no hook, then immediately builds the next prompt. | We drive **one story per `Loop` run** so we can verify the commit *before* marking done and switch branches while the agent process is provably dead. See §3.2. |
| `SetStoryStatus(id,"done")` also flips **every** unchecked acceptance-criteria checkbox to checked. | Acceptance criteria on disk are never a truthful record. PR bodies must snapshot the story *before* the flip, and the GUI should label them "auto-marked, not verified". |
| `git.FindCommitForStory` greps the entire log, so re-running a completed story matches an ancestor commit. | Add a `headBefore..HEAD` range check. Don't use the upstream function bare. |
| Stale `prd.json` paths at `app.go:787, 1379, 1417, 1698` break worktree-started PRDs and background auto-PR (`prd.LoadPRD` is markdown-only now). | Known upstream bug — do not port. |
| Claude Code 2.1.185 supports `--input-format stream-json` with `--replay-user-messages`, `--session-id`, `--resume`. | A real structured chat backend is feasible — but see §4 for why PTY ships first. |
| `gh stack` (2.68 installed, extension not yet) provides `init --base`, `add`, `submit --auto` (**creates drafts by default**), `view --json`, `rebase`, `sync`, `merge`, `link`. GitHub auto-retargets and rebases children when a layer merges. | Most of the stacking machinery is free. See §3. |
| Every provider hardcodes a permission bypass (`--dangerously-skip-permissions`, `--yolo`, `--force --trust`). | A GUI makes unattended runs much easier than a terminal does. Needs an explicit, visible disclosure — see §7. |
| Go and `wails3` are **not installed**; Node 24.14.1 / npm 11.11 are. | M0 is toolchain setup. |

---

## 1. Vendoring chief

Pinned git submodule at `third_party/chief`, plus `scripts/sync-upstream.sh` that regenerates `internal/chief/{loop,agent,prd,git,config}` with import rewriting. `github.com/minicodemonkey/chief/embed` stays a normal `require` — it's public.

The key move: once chief's `loop` package lives at `internal/chief/loop`, we can add **new files to that same package** (`loop_ext.go`) with full access to unexported identifiers (`buildPrompt`, `runIterationWithRetry`, `sawStoryDone`). Everything we need is additive, so **`patches/` is empty in v1** and re-sync is pure regeneration.

```
scripts/sync-upstream.sh v0.8.1
  1. submodule fetch --tags && checkout <tag>
  2. delete files listed in internal/chief/UPSTREAM.manifest; copy upstream pkgs
     (INCLUDING *_test.go and testdata/)
  3. sed 's|minicodemonkey/chief/internal/|dripcoder/loop/internal/chief/|g'
     (leave .../chief/embed alone — real dependency)
  4. cp LICENSE → internal/chief/LICENSE
  5. regenerate UPSTREAM.manifest (path + sha256 + tag + commit + date)
  6. git apply --3way patches/*.patch   # loud failure; empty in v1
  7. go build ./... && go test ./internal/chief/...
```

- CI gate `task verify-vendor` re-syncs into a temp dir and `diff -r`s against `internal/chief/`, **excluding `*_ext.go`**. Any hand-edit to vendored code fails the build.
- Copying `*_test.go` means upstream's suite (loop, manager, all five parsers, PRD round-trip, worktrees) runs in our CI — ~4,000 lines of free regression coverage against our rewrite and against upstream drift.
- Attribution: verbatim `internal/chief/LICENSE`, root `NOTICE` naming chief + tag + "Copyright (c) 2026 Mathias Hansen", `THIRD_PARTY_LICENSES/` surfaced in the About window. No implication of endorsement.

*Rejected:* git subtree (still needs the rewrite, then conflicts on every rewritten import line, forever) · copy-and-own fork (upstream is active and provider CLI flags churn — we want those fixes free) · upstream PR promoting `internal/`→`pkg/` (worth opening in parallel for `loop`+`prd`, never depend on it).

---

## 2. `internal/session` — the headless orchestrator

Replaces `app.go`'s policy. **Never imports Wails, never blocks on a consumer, fully driveable from a Go test and from `cmd/loopctl`.** `internal/services` is a thin delegation-only adapter.

```go
func New(Options) (*Session, error)
func (s *Session) Events() <-chan Event            // single ordered, seq-numbered stream
func (s *Session) Replay(sinceSeq uint64) ([]Event, bool)
func (s *Session) Snapshot() Snapshot

func (s *Session) OpenProject(ctx, root) (Project, error)
func (s *Session) Start(ctx, StartRequest) (RunID, error)   // returns immediately
func (s *Session) Pause(RunID) / Resume / Stop / SetAttemptBudget
func (s *Session) Answer(QuestionID, Answer) error           // see below
func (s *Session) RetryGit(RunID, storyID, GitAction) error
```

**Blocking decisions become data, not dialogs.** The branch-safety tree from `app.go:726` and every other prompt is emitted as a `Question{Kind, Title, Options[], Inputs[], DefaultOption}` and resolved via `Answer()`. Kinds: `branch-safety`, `worktree-exists`, `dirty-worktree`, `story-no-commit`, `branch-conflict`, `pr-exists`, `quit-with-running`. Tests answer synchronously; the GUI answers whenever. Nothing blocks.

**Backpressure is non-negotiable.** `loop.Events()` is buffered 100 and the producer *blocks* when full — a slow webview would stall the agent. A dedicated drain goroutine per `Loop` appends to a ring buffer and does a non-blocking send. Agent text/tool-result events coalesce (drop-oldest) under pressure; **every other kind is guaranteed**. Any drop emits one `stream.dropped` so the UI can `Replay(seq)` or `Snapshot()`.

Files: `session.go` (bus, ring, replay) · `run.go` (state machine, watchers) · `story.go` (preflight → agent → verify → mark → stack → next) · `policy.go` (branch safety, attempt budget, in-progress cleanup) · `worktree.go` · `stack.go` (§3) · `verify.go` · `state.go` · `prdlock.go` · `question.go`.

Two policies ported verbatim from `app.go`, now unit-testable:
- **Attempt budget** `max(5, incomplete+5)`, recomputed on PRD reload only when no run is active.
- **In-progress cleanup** on every terminal event — *plus* on `OpenProject`, which upstream never does, so a crashed chief leaves a story wedged as in-progress forever.

---

## 3. Stacked per-story branches + draft PRs

### 3.1 Drive `gh stack`, don't reimplement it

GitHub's native stacks handle the hard parts: each PR targets the layer below, and when a layer merges, **the PRs above it stay open and automatically rebase and retarget**. That deletes the restacking engine and the base-retargeting fallback I'd otherwise have to build.

Per-run sequence:

```
run start   gh stack init --base <trunk> loop/<prd>/us-001-<slug>   # adopts or creates, checks out
story done  (verify commit)  →  gh stack submit --auto              # pushes + creates DRAFT PRs
            gh pr edit <n> --title … --body-file -                  # our story-derived content
            gh stack add loop/<prd>/us-002-<slug>                   # branch at HEAD, checkout, top of stack
            → continue immediately, no waiting
```

`gh stack submit --auto` skips the interactive editor, is CI-safe, and **creates new PRs as drafts unless `--open` is passed** — exactly the requested behavior. Its only cost is auto-generated titles, fixed by the `gh pr edit` immediately after.

`gh stack view --json` is the reconcile source. `gh stack merge`, `rebase`, `sync --prune`, and `unstack` back the GUI's merge/cleanup actions.

**Fallback path (required, not optional):** if the extension isn't installed, fall back to hand-rolled `git push -u` + `gh pr create --draft --base <prev-branch> --head <branch> --body-file -`. Config key `git.stackDriver: auto | gh-stack | manual`. `Environment()` probes for the extension at startup and offers a one-click `gh extension install github/gh-stack`.

### 3.2 One story per `Loop` run — no patching

`internal/chief/loop/loop_ext.go` (our file, same package, never overwritten):

```go
// NewStoryLoop builds a Loop that runs the agent for exactly one story.
// Unlike Run, RunStory does NOT write story status — the caller decides,
// after verifying the agent actually committed.
func NewStoryLoop(prdPath, workDir string, provider Provider) *Loop
func (l *Loop) RunStory(ctx context.Context) (StoryRun, error)  // ~50 lines
```

`RunStory` mirrors `Run`'s body minus the outer loop and minus the `SetStoryStatus(…,"done")` call, reusing `runIterationWithRetry` so crash-retry and watchdog behavior are identical.

Why this beats patching `Run` with a completion hook:
1. **Commit verification must precede the status flip.** A post-`SetStoryStatus` hook has already written `done` *and auto-checked every acceptance criterion* before you know whether the agent committed.
2. **Branch switching needs a provably dead agent process.** `checkout -b` while the agent is alive is the scariest failure mode here. With one-story runs, `cmd.Wait()` has returned before we touch git. `run.go` additionally asserts `!loop.IsRunning()` before any git mutation.
3. Zero patch maintenance; per-story budgets, timings, and cancellation become first-class instead of inferred from `EventIterationStart` transitions.

Cost: ~80 lines reimplementing "retry the same story" and `AllComplete → complete` termination — logic we want to own anyway, since upstream conflates iteration budget with retry budget.

### 3.3 Commit verification

```
before:  git rev-parse HEAD                       → headBefore
         git status --porcelain                   → must be clean (else ask)
 after:  git rev-list --count headBefore..HEAD
         git.FindCommitForStory(dir, id, title)   → hash
         verify hash ∈ headBefore..HEAD           ← fixes upstream's false positive
```

| Verdict | Action |
|---|---|
| Committed, subject matched | mark done → submit → PR edit → next branch |
| Committed, subject didn't match | **accept** with a warning. The subject is a convention, not a contract. |
| No commit, done sentinel seen | Ambiguous (may be a genuine no-op). Ask `story-no-commit`: `mark-done-no-pr` (default) / `retry` / `mark-blocked`. On no-op, carry the branch pointer forward and skip push/PR. |
| No commit, no sentinel | Attempt failed. Consume one attempt, leave `in-progress`, retry same story. Budget exhausted → story failed, run pauses. |
| Leftover dirty files | Never auto-commit. Warn, listing paths; `checkout -b` carries them into the next story branch, which is correct. |

### 3.4 Failures are loud but never fatal

"Continue immediately" was chosen, so no git failure stops the loop. Every git step runs through `tryGit(op, fn)`, which emits a `GitEvent{state:"error", Fatal:false}` and records it in state. The UI surfaces a per-story red chip with a `RetryGit` action plus a run-level "3 git actions need attention" banner — **not a toast**, because toasts get missed and this is precisely the class of failure the user must see.

### 3.5 Worktrees are required in per-story mode

The PRD runs in `.chief/worktrees/<prd>/`. Per-story branch switching inside the project root would yank the user's own checkout out from under them, so `git.requireWorktree: true` is the default for `per-story` and the branch-safety question's recommended option flips to "Create worktree + branch".

Two deviations from upstream worktree provisioning: stream the setup command's output line-by-line (a silent 3-minute `npm install` is unacceptable in a GUI), and run it under a context so Cancel actually kills it — upstream's `CombinedOutput()` can't be cancelled, so ESC during setup only pretends to.

Per-story switch handles: detached HEAD (`checkout -B <expected> HEAD` + loud warn), dirty tree (warn, proceed, changes carry forward), existing branch (fast-forwardable → checkout; diverged → `branch-conflict` question), and the agent having created its own branch (detect via `symbolic-ref` delta, stack from *there* and warn — fighting the agent loses).

### 3.6 Config

```yaml
agent:    { provider: claude, cliPath: "" }
worktree: { setup: "npm ci" }
git:
  mode: per-story          # off | per-prd | per-story    (default: per-prd)
  stackDriver: auto        # auto | gh-stack | manual
  baseBranch: ""           # "" → repo default branch
  branchTemplate: "loop/{prd}/{story}-{slug}"
  draft: true
  requireWorktree: true
  verifyCommit: true
onComplete: { push: false, createPR: false }   # LEGACY, honoured only when mode == per-prd
```

Absent `git:` block → synthesize `per-prd` from the legacy `onComplete` keys. chief's TUI ignores unknown YAML keys, so **the same `.chief/` works in both tools** — a real requirement, since users will keep using the TUI.

### 3.7 Resume — state is a cache, git is the truth

`.chief/prds/<name>/loop-state.json` (schemaVersion, run metadata, and per story: `branch`, `prBase`, `headBefore/After`, `commits`, `verdict`, `attempts`, timings, `pr{number,url,state,draft}`, `errors[]`, `titleHash`).

- Single writer (the run goroutine), in-process mutex + `flock`, atomic temp-file rename. Stale locks detected via `owner.pid` + boot ID.
- **`Reconcile()` on every `OpenProject` and before every `Start`.** Branch names are deterministic (`kebab(title)`, ASCII-folded, 48 chars, validated with `git check-ref-format`), so we recompute them from `prd.md` and ask git and `gh stack view --json` for the truth. **Deleting `loop-state.json` is fully recoverable** — you lose only timings and error history. That's what makes this safe to bolt onto a tool with no state file at all.
- Crash recovery: `state == running` with a dead pid → mark interrupted, reset in-progress stories, offer "Resume from US-004?".
- `titleHash` mismatch (PRD edited between runs → derived branch name changed) → `branch-conflict` question, default reuse-old.
- Refuse per-story mode if `.chief/` is tracked in git — the agent would commit our state file into its own story branches. Reuse `git.AddChiefToGitignore`.

### 3.8 The prompt must change

`embed/prompt.txt` tells the agent to commit but says nothing about branches. Our own `internal/prompts/story.txt` adds:

> **Branching.** You are already on the correct branch for this story. Do NOT create, switch, rebase, merge, or delete any git branch. Do NOT push. Commit only.

A test diffs our copy against `embed.GetPrompt` so upstream prompt improvements are visibly pending rather than silently missed.

### 3.9 Idempotency

Every step is re-runnable: branch creation checks `rev-parse --verify` first · `CreateWorktree` already reuses valid worktrees · `git push -u` is idempotent · PR creation does `gh pr list --head` first and edits rather than duplicates (closed/merged → warn, never reopen) · already-done stories are skipped by `NextStory()`.

---

## 4. Embedded PRD authoring

One `authoring.Chat` interface, two backends:

- **PTY** (`creack/pty` + `provider.InteractiveCommand`) → xterm.js. Works for all five providers.
- **Structured** (Claude only): `claude --print --input-format stream-json --output-format stream-json --replay-user-messages --session-id <uuid> --permission-mode acceptEdits`. Verified available in Claude Code 2.1.185. Needs its own parser — upstream's `ParseLine` silently drops `stream_event` partials.

**Ship PTY for all five providers in v1; structured Claude in v1.1 behind a toggle.** One implementation covers 100% of providers, and it's behaviorally identical to the TUI — auth prompts, `/login`, model pickers, the agent's lettered clarifying questions, and Ctrl-C all work on day one. The structured path has genuine unknowns (permission handling under `-p`, how partial-message streaming interleaves with replayed user messages) that are v1.1 problems, not v1 blockers. The abstraction makes v1.1 a constructor change.

A third trivial `oneshot` backend handles `detect_setup_prompt.txt` for the first-run wizard.

Both backends share the post-flight from `cmd.RunNew`/`RunEdit`: stat `prd.md`, parse it, and for `new` remove the empty dir when nothing was created.

---

## 5. Wails v3 boundary

Six services, all bound methods coarse-grained and fast; anything slow returns immediately and reports via events.

| Service | Methods |
|---|---|
| `ProjectService` | `Open`, `PickAndOpen`, `Recent`, `Environment`, `GetConfig`, `SaveConfig`, `DetectSetupCommand` |
| `PRDService` | `List`, `Get`, `Progress`, `SetStoryStatus`, `Delete`, `Validate`, `RevealInFinder` |
| `RunService` | `Start`, `Pause`, `Resume`, `Stop`, `SetAttemptBudget`, `Answer`, `Snapshot`, `Replay`, `LogTail`, `RetryGit` |
| `AuthoringService` | `Begin`, `Send`, `WriteKeys`, `Resize`, `Interrupt`, `End`, `Transcript` |
| `GitService` | `Status`, `Stack`, `StoryDiff`, `Worktrees`, `RemoveWorktree`, `Merge`, `OpenPR` |
| `SystemService` | `Version`, `CheckForUpdate`, `OpenExternal`, `Credits`, `LogPath` |

**Four event names only:** `loop:event` (the ordered, seq-numbered firehose), `loop:snapshot` (≤2 Hz coalesced resync), `authoring:event`, `pty:data` (base64, ~16 ms coalescing).

Wails' `Emit` gives **no cross-name ordering guarantee and no delivery confirmation**. Multiple named channels would let the frontend observe `git:push` before `story:started`. One ordered channel plus snapshot/replay is the only design that stays correct under a lossy transport.

**Hard requirement on the Go side:** the log firehose must coalesce — flush every 50 ms or 256 events, whichever comes first. One `Emit` per log line at 500 lines/sec is death by IPC.

---

## 6. Frontend

**Svelte 5 (runes) + TypeScript + Vite + Tailwind v4.** Wails v3 ships an official `svelte-ts` template. The decisive factor is the log stream: at 200–2000 events/sec the framework must not be in the hot path, and `$state.raw` lets me hold a 20k-element ring buffer *outside* the reactive graph with a single `version` signal driving the virtualizer. React can reach the same place, but only by being careful; here it's the default. Secondary: 12 KB runtime vs 45 KB, and every heavy dep (TanStack Virtual, Shiki, CodeMirror, xterm, tinykeys, jsdiff) is framework-agnostic anyway.

**No router.** There are no URLs and no history semantics. One `ui.svelte.ts` view machine persisted to `localStorage`.

### The IA rethink

The TUI is modal because a terminal has one surface and no z-order. Three of its modals exist purely as that workaround, and porting them literally would be the templated-Electron mistake:

- **The PRD picker is not a dialog — it's the app's primary navigation.** It shows live per-PRD loop state with ticking iteration counters, i.e. it's admitting you need to *watch* it, in a surface that occludes everything. → **permanent left rail**, absorbing the tab bar (which is the same list rendered twice).
- **The completion screen is a full-screen takeover in a system with parallel loops.** PRD A finishing shouldn't evict your view of PRD B mid-run. → **a Summary tab on the owning PRD**, plus a toast.
- **The help overlay is a modal because there's nowhere else to put discovery.** → **command palette** (⌘K), searchable and executable, with a generated cheat-sheet.

Rule for `CONTRIBUTING.md`: *a modal is only correct when the app cannot proceed until the user chooses. Progress is not a modal. Navigation is not a modal. Results are toasts or panels.* What stays modal: branch-safety, clean-worktree (destructive, 3-way), merge conflict, quit confirm. First-run is a full-window takeover, not a modal.

```
┌────────────────────────────────────────────────────────────────────────────┐
│ ● ● ●   loop                                             ⌘K    ?    ⚙      │
├────────────────────────────────────────────────────────────────────────────┤
│▓▓▓▓▓▓▓▓▓▓▓▓▓░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░│ ← run bar
├──────────────┬──────────────────────────────────────────┬──────────────────┤
│ PRDS      +  │ ▍RUNNING  iter 12/50  04:12   ⏸  ⏹  max─+│  US-003          │
│              │ Stories │ Log │ Diff │ PRD │ Author │ Sum │ ● In progress    │
│●checkout   ⣾ │ ✓ US-001  Parse PRD markdown             │ priority 3       │
│ ███████░░ 5/8│           ⎇ us-001 → main   ⬀ #12 draft  │                  │
│ ⎇ loop/check │ ✓ US-002  Watch prd.md                   │ ACCEPTANCE       │
│              │           ⎇ us-002 → us-001 ⬀ #13 draft  │ ☑ header shows…  │
│○ docs-site   │ ● US-003  Render dashboard          ⣾    │ ☐ resizes…       │
│ ███░░░░░ 2/6 │           ⎇ us-003 → us-002 ↑ pushing…   │                  │
│ idle         │ ○ US-004  Log viewer                     │ PROGRESS LOG     │
│              │ ○ US-005  Diff viewer           ⚠ PR ✗   │ 14:02 committed… │
│ ⚙ Settings   │                                          │ 14:09 tests pass │
├──────────────┴──────────────────────────────────────────┴──────────────────┤
│ ⣾ Edit  internal/tui/log.go                    3 loops running   ⌘K   ? ⌨  │
└────────────────────────────────────────────────────────────────────────────┘
```

The center header (state / iteration / elapsed / transport) is **per-PRD**, not global — with parallel loops a global header would lie. Stacked-PR state renders as a second line under each story title. Resize: three columns ≥1180 px; inspector becomes an overlay drawer below that; rail collapses to a 48 px icon strip under 940 px.

### The hot path

```
Events.On('loop:event') → push batch into pending[]  (no reactivity touched)
  → single rAF-scheduled flush → append to plain Array ring (NOT $state)
     → detect seq gaps → Replay/Snapshot
     → trim to 20k → bump ONE version signal
```

rAF coalescing caps UI updates at 60/sec regardless of event rate, and browsers pause rAF when the window is occluded — free backpressure. Autoscroll disengages on any upward scroll and shows a floating `↓ 1,284 new` pill; the virtualizer pins its anchor by index+offset so trims don't jump. Per-PRD buffers are **retained across PRD switches** (the TUI clears them, which with parallel loops is data loss). Full history goes to `.chief/logs/<prd>-<runid>.jsonl` in Go — that buys whole-run search, export, and survival across a webview reload, and lets the memory cap stay aggressive.

Rendering: **Shiki** core + JS RegExp engine (skipping WASM Oniguruma saves ~1.2 MB) in a **Web Worker**, lazy grammars, custom theme matching our tokens. Tool cards use inline SVG, **not the TUI's emoji** — emoji widths differ across platforms and wreck the alignment that makes a log scannable. Diff: `parse-diff` + a hand-rolled virtualized renderer (diff2html can't be virtualized or themed), unified by default with a side-by-side toggle ≥1200 px, a per-file rail with `[`/`]` navigation, and lazy word-level intra-line diff.

Keyboard: one **command registry** is the single source of truth for the ⌘K palette, the `?` cheat-sheet, status-bar hints, and the native menu — so the docs can't drift from the bindings. `tinykeys` + an `isEditableTarget` guard + a scope stack (`global → log → dialog`) so `x` in a confirm dialog doesn't stop your loop. Every single-letter TUI key also gets a `$mod` alias.

Visual direction: OKLCH tokens, dark-first, keeping the TUI's semantics (cyan active, green passed, amber paused, red error) but equalizing lightness and chroma — upstream's green is far brighter than its red, so errors currently read *quieter* than successes. Inter + JetBrains Mono NL (no ligatures — they mislead in diffs), `tabular-nums` on every changing number. **Motion means work is happening:** the 2 px run bar animates only while a loop runs, and nothing else moves. Confetti becomes a single 2.2 s focused burst instead of the TUI's continuous animation — hostile in a window you leave open for hours.

`frontend/src/platform/` is a hard boundary: nothing else imports `@wailsio/runtime`. That's what lets `mock.ts` replay a recorded JSONL run in a plain browser, so tweaking a border radius doesn't need a Go rebuild — and it's what contains a Wails v3 API change to four files.

---

## 7. Repository layout

```
loop/
├── go.mod                     module github.com/dripcoder/loop  (go 1.24)
├── Taskfile.yml               Makefile wraps it for muscle memory
├── LICENSE  NOTICE  README.md  THIRD_PARTY_LICENSES/
├── .gitmodules                third_party/chief @ v0.8.0
├── cmd/
│   ├── loop/main.go           Wails app: window, services, event bridge
│   └── loopctl/main.go        headless CLI over internal/session (e2e harness)
├── internal/
│   ├── chief/                 ── VENDORED, GENERATED, DO NOT HAND-EDIT ──
│   │   ├── UPSTREAM.manifest  path + sha256, CI-verified
│   │   ├── loop/  + loop_ext.go       (NewStoryLoop, RunStory)
│   │   ├── prd/   + status_ext.go     (locked SetStoryStatus)
│   │   ├── git/   + worktree_ext.go   (CreateWorktreeFrom)
│   │   ├── config/+ config_ext.go     (git: block + migration)
│   │   └── agent/
│   ├── session/               ← the heart (§2, §3)
│   ├── ghstack/               gh stack driver + manual fallback (§3.1)
│   ├── authoring/             pty.go · streamjson.go · oneshot.go
│   ├── prompts/               our //go:embed story prompt (§3.8)
│   ├── projects/  logx/  services/
├── patches/                   EMPTY in v1 (machinery kept as insurance)
├── scripts/sync-upstream.sh
├── third_party/chief/         submodule
├── build/                     wails3 assets, Info.plist, entitlements
└── frontend/                  Svelte 5 + TS + Vite; bindings/ generated
```

### Toolchain (nothing compiles on this machine today)

```bash
brew install go                                    # >= 1.24
xcode-select --install
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha2.114   # PIN EXACTLY
go install github.com/go-task/task/v3/cmd/task@latest
gh extension install github/gh-stack
wails3 doctor
```

Task targets: `dev` · `build` · `package` · `bindings` · `sync-upstream` · **`verify-vendor`** · `test` · **`test-race`** (mandatory — `session` is concurrency-heavy) · `e2e` · `lint`. CI on macOS + Linux; `verify-vendor` and `test-race` are required checks.

**Testing `internal/session` is what makes or breaks this.** A `fakeprovider` implementing `loop.Provider` whose `LoopCommand` is a scripted `sh -c` fixture: emits canned NDJSON, optionally makes a real commit, optionally emits the done sentinel, optionally exits non-zero or hangs (watchdog). `gh` is behind an interface with a fake that records invocations. Every test runs against a **real temp git repo** — git is cheap, and mocking it produces tests that pass while the product is broken. The full state machine runs through `loopctl` in `task e2e`, including kill-mid-story-then-resume.

---

## 8. Milestones

| # | Deliverable | Exit criterion |
|---|---|---|
| **M0** | Toolchain + skeleton | `wails3 doctor` clean; empty window builds; CI green |
| **M1** | Vendor pipeline | Upstream's whole test suite passes under `internal/chief/`; `verify-vendor` gate live; licences in place |
| **M2** | `session` read-only + `loopctl` | Open project, scan PRDs, parse progress, watchers, `Environment()`. Zero GUI. |
| **M3** | GUI read-only | Rail, story list, inspector, run bar, elapsed. Firehose + seq/replay wired end to end. Watch a run the TUI started. |
| **M4** | Log view | Ring buffer, rAF flush, gap resync, virtualization, all row kinds, autoscroll + pill. **No highlighting yet.** Load-test with a 100k-event fixture at 5× — if this fails, the stack choice is wrong and we learn it on day ~20, not day 60. |
| **M5** | Run a PRD, `git.mode: off` | `RunStory`; per-story attempts + timings; pause/stop; in-progress cleanup; command registry + ⌘K; **fakeprovider e2e green**. **First genuinely useful build — dogfood from here.** |
| **M6** | Questions + worktrees | Branch safety as data; streamed cancellable setup; `git.mode: per-prd` (upstream parity, minus the `prd.json` bugs) |
| **M7** | **Stacked PRs** (§3) | `gh stack` driver + manual fallback; commit verification; `loop-state.json` + flock + `Reconcile()`; non-fatal error surfacing + `RetryGit`. E2E: kill mid-PRD, reopen, resume, correct stack on GitHub. |
| **M8** | Highlighting + log search | Shiki worker, custom theme; filter bar + search over the JSONL |
| **M9** | Diff view | Parser, virtualized unified renderer, file rail, word-diff, side-by-side |
| **M10** | Authoring (PTY) | `new`/`edit` embedded; xterm.js; outcome validation; first-run wizard |
| **M11** | Parity polish | Settings incl. the `git:` block and agent provider (new); Summary view with timings + tamed confetti; merge/clean; multi-PRD parallel runs; update check |
| **M12** | Packaging | Signed + notarised .dmg, auto-update, About with licences |
| **M13** | v1.1 | Structured Claude chat; upstream PR promoting `internal/`→`pkg/` |

Rough sizing: **M5 ≈ 20 days, full parity ≈ 45, M12 ≈ 55** for one person who knows the stack. M4 before M5 is deliberate — the perf risk must be retired early.

---

## 9. Verification

- **Unit** — `go test ./...` covers the vendored upstream suite plus `session` policy (attempt budget, branch-safety tree, verdict table, reconcile). `task test-race` on `session`.
- **E2E, no API cost** — `task e2e` drives `loopctl` with the `fakeprovider` against a throwaway `git init` repo: full PRD run, per-story branches, commit verification across all four verdicts, kill-mid-story → reopen → resume, and idempotent re-run.
- **Stacked PRs against real GitHub** — a scratch repo, `-tags integration`. Assert: N stories → N draft PRs, each based on the one below, linked as a GitHub Stack, `gh stack view --json` matching `loop-state.json`. Then merge the bottom PR and confirm GitHub auto-retargets the rest.
- **Manual, the real test** — point Loop at a real project with a 5-story PRD and Claude Code, watch it run end to end, review the resulting stack. Cross-check the same `.chief/` still opens correctly in the chief TUI.
- **Frontend perf** — replay a 100k-event JSONL at 5× in the browser harness; assert sustained 60 fps and a stable heap. Run the log view on WebKitGTK by M4, not at the end.
- **Parity audit** — `docs/parity.md`, a checklist keyed to the TUI source file each feature came from, checked in review.

---

## 10. Risks

- **Wails v3 is alpha** (nightly `alpha2.x`). Service/event/window APIs are declared stable, but pin the exact version and treat upgrades as scheduled work. `Emit` has no delivery guarantee, which is why the seq/replay/snapshot machinery is mandatory rather than polish. The `platform/` boundary means an API change touches four files; the fallback to Wails v2 is ~2 days since nothing depends on v3-specific features.
- **`gh stack` is public preview**, two days old. Flag semantics may shift and merge-queue support is still rolling out. Mitigated by the manual `gh pr create --draft --base` fallback, which must stay tested, not vestigial.
- **Draft PRs on private repos** historically require a paid plan. If `gh` fails with a plan error, fall back to a normal PR titled `[Draft] …` and record it. Unverified against your org.
- **9 stories → 9 PRs.** Some teams will find that intolerable, and reviewers see cumulative diffs unless they use the compare view. `git.mode: per-prd` and `stackDriver: manual` are the escape hatches; expect the setting to matter.
- **`prd.md` races.** Our flock only coordinates our own processes. A user editing `prd.md` in their editor during a run can still lose the status write. Mitigate with an mtime+size compare-and-swap and a question; don't claim it's airtight.
- **Permission-bypass flags.** Every provider hardcodes `--dangerously-skip-permissions` / `--yolo` / `--force --trust`. A GUI makes it far easier to run this unattended than a terminal does, which raises the stakes. Needs a visible security disclosure in the UI and an `Environment()` probe per provider — these flags break on CLI updates and the failure mode is an agent that silently does nothing.
- **WebKitGTK** lags on container queries and `oklch()`. Generate a hex fallback palette at build time; feature-detect container queries; no `backdrop-filter` or `:has()` in load-bearing selectors.
- **Bundle size.** Budget <900 KB gzipped initial, <3.5 MB total. Shiki grammars, CodeMirror, and xterm are all lazy chunks; enforce in CI.
- **Unverified** (no Go toolchain here): that the import-rewritten packages compile, that upstream's tests pass after rewriting, that `wails3` builds on this machine. M0/M1 exist to find out early and are cheap.
