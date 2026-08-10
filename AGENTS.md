# Working on Loop

Loop is a Wails v3 desktop GUI for [chief](https://github.com/MiniCodeMonkey/chief):
it loops a coding-agent CLI over the user stories in a markdown PRD, one commit
per story, and opens a stacked draft PR for each one.

Go 1.24 backend, Svelte 5 (runes) + TypeScript frontend, module
`github.com/dripcoder/loop`.

---

## Read this first: `internal/chief/` is generated

**Never hand-edit anything under `internal/chief/`.** It is chief's engine,
copied in by `scripts/sync-upstream.sh` with import paths rewritten. Two things
happen if you edit it:

1. `task verify-vendor` fails — it re-syncs into a temp dir and diffs. This is a
   required CI check, so the branch lands red.
2. The next `task sync-upstream` **silently deletes your new files and reverts
   your edits**. No conflict, no warning.

### How to extend it instead

Add a file named `*_ext.go` (or `*_ext_test.go`) in the same directory. The sync
script excludes that suffix, so those files survive. Being in the same Go package
means they can use the vendored code's unexported identifiers — which is the
whole point, and why `patches/` is empty and should stay that way.

Existing examples, worth reading before you add one:

| File | What it adds |
|---|---|
| `internal/chief/loop/loop_ext.go` | `NewStoryLoop` / `RunStory` — runs the agent for exactly one story, and deliberately does *not* write story status, so the caller can verify the commit first |
| `internal/chief/config/config_ext.go` | `LoopConfig` — chief's config plus our `git:` block, with migration |

Naming a new file `usage.go` instead of `usage_ext.go` is the mistake this
section exists to prevent. It has already happened once.

See [`internal/chief/UPSTREAM.md`](internal/chief/UPSTREAM.md) for the sync
mechanics.

---

## Layers

Each layer may only talk to the one below it.

```
frontend/src/           Svelte components + stores
  └ platform/index.ts   THE boundary — the only file importing @wailsio/runtime
internal/services/      Wails-bound services. Delegation only, ~5 lines/method.
internal/session/       The engine. All policy lives here. No Wails, ever.
internal/chief/         Vendored upstream. Generated.
```

Supporting packages: `internal/ghstack` (gh stack driver + manual fallback),
`internal/authoring` (PTY sessions for PRD writing), `internal/prompts`
(user-customisable prompt overrides), `internal/agentx` (process-group kill),
`internal/fakeagent` (scripted agent for tests).

### Rules that hold the design up

- **`internal/session` never imports Wails and never blocks on a consumer.**
  It is driven identically by the GUI, by `cmd/loopctl`, and by tests. If you
  find yourself needing a Wails type in `session`, the logic belongs in
  `services` or the type belongs in `session`.
- **Blocking decisions are data, not dialogs.** Branch safety, dirty worktree,
  no-commit — every one is a `Question` emitted on the event stream and resolved
  by `Answer()`. Tests answer synchronously; the GUI answers whenever. Nothing
  in the engine waits on a human by blocking a goroutine.
- **One ordered event stream.** `Session.Events()` yields sequence-numbered
  `Event`s. Wails' `Emit` gives no cross-name ordering guarantee, so a second
  event name would let the UI observe a push before the story that caused it.
  Use `Replay(sinceSeq)` / `Snapshot()` to recover from a gap.
- **The bus may only drop `agent.text` and `agent.toolResult`.** chief's
  `loop.Events()` is buffered 100 and *blocks its producer*, so anything
  downstream that blocks stalls the agent's stdout scanner. Everything except
  those two kinds is guaranteed delivery; drops are reported via `Event.Dropped`
  riding on the next delivered event.
- **`frontend/src/platform/index.ts` is a hard boundary.** Nothing else imports
  `@wailsio/runtime` or the generated bindings. That is what lets the whole UI
  run in a plain browser against `platform/mock.ts` — adjusting a border radius
  must not need a Go rebuild.
- **Go marshals nil slices as `null`.** Every list-returning binding is wrapped
  in `list()` at the platform boundary. Keep new ones wrapped.

---

## Adding a Go method the frontend calls

Four steps, and the codegen one is not optional:

1. Real logic as a method on `*Session` in `internal/session/`.
2. A ≤5-line delegation on the matching service in `internal/services/services.go`.
3. Regenerate bindings — they use `$Call.ByID(<hash>)` and **cannot be
   hand-written**:
   ```bash
   PATH=$PATH:$(go env GOPATH)/bin wails3 generate bindings -ts
   ```
   `frontend/bindings/` is gitignored, so this never shows in `git status`.
4. Surface it in `platform/index.ts` **and** add a mock in `platform/mock.ts`,
   or the browser build breaks.

---

## Commands

```bash
task test          # all Go tests, including chief's upstream suite
task test-race     # session, ghstack, authoring — these are concurrency-heavy
task verify-vendor # the gate described above
task lint          # go vet, excluding the vendored tree
task e2e           # loopctl vs. a scripted fake agent in a throwaway git repo
task ci            # everything CI runs
task dev           # Go hot-reload + Vite HMR
```

Frontend: `cd frontend && npm run check` (svelte-check) and `npm test` (vitest).
`task fmt` formats our Go and deliberately skips `internal/chief/`.

`cmd/loopctl` drives the engine headlessly — `loopctl doctor | list | show |
run | watch`. It is the fastest way to reproduce engine behaviour without
building the GUI, and it is what `task e2e` uses.

Tests run against **real temp git repositories**, not a mocked git. Mocking git
produces tests that pass while the product is broken.

---

## Where the context lives

- **`.chief/prds/<name>/progress.md`** — accumulated codebase patterns written by
  previous agent runs. Genuinely the densest description of this codebase's
  conventions; read the one for the PRD you are working on.
- **`docs/design.md`** — the original design document. Explains *why*, including
  findings about upstream chief that are still load-bearing. Predates the code.
- **`docs/parity.md`** — feature checklist against the chief TUI.
- **`docs/icons.md`** — icon sources vs. generated files, and why
  `build/appicon.icon/` must change alongside `build/appicon.png`.
- **`internal/chief/UPSTREAM.md`** — vendoring mechanics.

Comments in this codebase explain *why*, not *what*. When you change something
a comment justifies, update the comment or you have made the code lie.

---

## Conventions

- No `else` / `else if` — early return, or extract a function.
- No nested `if`. Functions ~20 lines, ≤3 parameters.
- Functions are verbs; booleans read as questions (`isRunning`, `hasCommit`).
- No abbreviations (`organizationId`, not `orgId`).
- No commented-out code, no magic numbers, no leftover debug output.
- Commits: no `Co-Authored-By`, no AI attribution. PRs are created `--draft`.

## Security posture

chief's providers all launch the agent with permission checks disabled
(`--dangerously-skip-permissions`, `--yolo`, `--force --trust`). Loop inherits
that, and a GUI makes unattended runs far easier than a terminal does. This is
disclosed in the README and in the Settings view — **keep it disclosed.** Do not
add a code path that broadens what an agent can reach without saying so in the
UI. Per-story mode defaults to `requireWorktree: true` for this reason.
