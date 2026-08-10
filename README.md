# Loop

A desktop GUI for [chief](https://github.com/MiniCodeMonkey/chief) — build big
projects by looping a coding agent over the user stories in a markdown PRD.

Loop reuses chief's engine verbatim and replaces its terminal UI with a native
window, adding two things a TUI can't do:

- **Embedded PRD authoring.** `chief new` / `chief edit` hand the terminal over
  to an interactive agent session. Loop runs that conversation in-app.
- **Per-story stacked branches and draft PRs.** Instead of one branch and one PR
  per PRD, every story gets its own branch stacked on the previous one, pushed
  with a draft PR the moment it completes — built on
  [GitHub's stacked pull requests](https://github.blog/changelog/2026-07-30-stacked-pull-requests-are-now-in-public-preview/).

Loop reads and writes the same `.chief/` directory as the chief TUI, so you can
switch between the two on the same project.

> **Status: alpha.** Running PRDs, per-story stacked draft PRs, the log view and
> embedded PRD authoring all work; polish and packaging do not. See
> [docs/parity.md](docs/parity.md) for what is and isn't built, and
> [docs/design.md](docs/design.md) for why it is shaped this way.

Contributing — including the one rule that will bite you (`internal/chief/` is
generated) — is in [AGENTS.md](AGENTS.md).

## Requirements

- Go 1.24+
- Node 20+
- [`wails3`](https://v3.wails.io) (pinned — see `Taskfile.yml`)
- `git`, and `gh` 2.0+ for pull-request features
- A coding agent CLI: Claude Code, Codex, OpenCode, or Cursor
  (chief v0.8.0 ships these four; Gemini landed upstream after the tag and
  arrives with a future `task sync-upstream`)
- For stacked PRs: `gh extension install github/gh-stack`

## Development

```bash
task setup      # install pinned wails3 + sync vendored chief
task dev        # Go hot-reload + Vite HMR
task test       # includes chief's own upstream test suite
task e2e        # headless run against a scripted fake agent
```

Run `task --list` for everything else.

## Documentation

- [docs/workflows.md](docs/workflows.md) — authoring sessions, the PRD sidebar
  and its actions, repository launchers, per-phase agents, and stacked pull
  requests.
- [docs/usage.md](docs/usage.md) — what the token and cost figures mean, and
  which fields each agent CLI actually reports.
- [docs/parity.md](docs/parity.md) — feature checklist against the chief TUI.
- [docs/design.md](docs/design.md) — why the code is shaped the way it is.
- [docs/icons.md](docs/icons.md) — which icon files are sources, which are
  generated, and how to change the app icon.

## Security note

chief's agent providers all launch with permission checks disabled
(`--dangerously-skip-permissions`, `--yolo`, `--force --trust`). Loop inherits
that: **an agent driven by Loop can run arbitrary commands and edit arbitrary
files in the project directory without asking.** A GUI makes unattended runs
much easier than a terminal does, so run it only against repositories you would
hand to an autonomous agent, and prefer git worktree mode.

## License

MIT. Vendors MIT-licensed source from chief — see [NOTICE](NOTICE).
