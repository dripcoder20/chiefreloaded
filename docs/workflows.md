# Workflows

How PRD authoring and implementation work in Loop, and what each control does.

For the usage and cost display, see [usage.md](usage.md). For why the code is
shaped the way it is, see [design.md](design.md).

---

## Authoring sessions

An authoring session is a live agent process with a real terminal attached,
used to write or revise a PRD. It is a real terminal on purpose: slash
commands, permission prompts, lettered clarifying questions and `Ctrl-C` all
work because nothing is being reinterpreted.

### Sessions survive tab switches

Switching to Stories or Settings and back does not terminate, restart or
replace the agent. The pane stays mounted and hides itself, so the process,
the scrollback, the session status and any unsent input are exactly as you
left them. Output produced while the tab was hidden appears when you return.

A session is released when the pane is genuinely torn down — the application
closing — or when you press **Stop**. Ordinary navigation never releases it.

### Multiline input

- `Enter` submits the prompt, including any line breaks in it.
- `Shift+Enter` inserts a line break without submitting.

Both work in New PRD and Edit PRD sessions. Input-method composition is not
mistaken for a submission, so composing candidates are safe.

### New PRD

Available three ways, all the same action:

- the **New PRD** button at the top of the PRD sidebar
- **File ▸ New PRD**
- `⌘N` on macOS, `Ctrl+N` on Windows and Linux

The shortcut is ignored while a modal question is waiting for an answer.

### Edit PRD

**Edit PRD** on a PRD's action menu opens a conversational editing session for
that PRD, with the current document as context. The tab is titled `Edit PRD`,
never `New PRD`, and the pane names the PRD being edited — which is what tells
two editing sessions apart.

Opening Edit PRD does not modify the file. Changes are written only when the
conversation reaches its own explicit save point. A PRD that cannot be read
reports an error instead of opening a session that would write an empty
replacement over it.

Selecting a different PRD in the sidebar does not re-point a live editing
session at another document.

---

## The PRD sidebar

**New PRD** and the repository launchers sit above a divider; the PRDs are
below it.

### Per-PRD actions

Every PRD row has a three-dot overflow button, revealed on hover or keyboard
focus and always visible on touch devices. Right-clicking the row opens the
same menu. Both offer the same three actions, in this order:

| Action | What it does |
|---|---|
| Edit PRD | Opens a conversational editing session for that PRD |
| Open markdown file | Opens its `prd.md` with your system's default markdown application |
| Delete PRD | Removes the PRD directory, after confirming |

Either menu acts on the PRD whose row opened it, even when a different PRD is
selected. Opening one closes any other. `Escape`, an outside click, focus
leaving the menu, or completing an action all dismiss it.

**Open markdown file** does not start an editing session and does not change
the active tab. A missing file, or no application associated with markdown,
produces an error naming the file rather than opening something else.

**Delete PRD** asks first, naming its target. It is refused while that PRD has
an implementation run or an authoring session attached — Loop will not silently
terminate work you have running — and the refusal says which session is in the
way. A refused or failed delete leaves the sidebar exactly as it was.

### Repository launchers

Three icon buttons beside New PRD:

| Button | What it does |
|---|---|
| **Open GitHub repository** | Opens the repository's GitHub page in your default browser |
| **Open in VS Code** | Opens the repository root in VS Code |
| **Open in AI IDE** | A dropdown of Claude, Cursor and Codex |

The GitHub remote is resolved from the project's `origin`. HTTPS, scp-style
SSH (`git@github.com:owner/repo.git`), `ssh://` and `git://` forms are all
accepted, and any credential embedded in the remote is stripped before the URL
is opened. A project with no remote, a malformed one, or a non-GitHub host
opens nothing and explains why.

The AI IDE dropdown always lists all three applications whether or not they
are installed. Hiding an entry would leave you with no control to activate and
therefore no explanation; selecting an unavailable one raises an alert naming
what to install. Availability is detected before launching, and the same
resolved executable is what gets launched — an application can never be
reported installed and then launch something else. "Not installed" and "found
but would not open" are distinct errors.

Repository paths are passed as discrete arguments, never interpolated into a
shell command.

---

## Agents

Loop has two phases and an agent for each. The best agent for a long
interactive conversation is not necessarily the best one for a long unattended
run.

### Configuring the defaults

`.chief/config.yaml`:

```yaml
agent:
  provider: claude   # the general default, as chief writes it

agents:
  authoring: claude
  implementation: codex
```

Either field under `agents:` may be omitted, in which case that phase uses
`agent.provider`. A project that has only ever set one agent keeps working
unchanged, with both phases agreeing until you say otherwise.

### Choosing per PRD

The New PRD tab offers **Authoring agent** and **Implementation agent** as
separate selectors, each initialised from its own resolved default — the actual
agent name, not a blank standing in for one. Changing one never moves the
other. Only installed agents are listed.

The authoring agent is used for that session. The implementation agent is saved
with the PRD and preselected when you start implementation, where it can still
be changed right up to pressing **Start**.

If a PRD's saved agent is no longer installed, starting is refused with an
error naming it. Loop will not quietly run a different agent than the PRD was
configured for.

### Seeing what is actually running

While a story is being implemented, its row shows the agent and model of the
session running it, sourced from the session rather than from configured
defaults — a session may use an override or a provider-selected fallback.

- A resolved value is shown as-is.
- `Resolving…` means a session exists but has not reported yet.
- `Unavailable` means the session reported and that provider does not supply
  the value. Several agent CLIs never name a model.

The indicator stays through starting, running, pausing, paused and stopping,
and is removed once the story has no active session. It is live state, not a
record of finished executions.

---

## Implementation workflow

### Stacked pull requests

The New PRD tab's **Stack a pull request per user story** option is off unless
you turn it on. It is saved as PRD metadata and applied when implementation
starts; choosing it creates no branches and no pull requests by itself.

### Where workflow settings live

Per-PRD settings are stored in a sidecar beside the document:

```
.chief/prds/<name>/loop.json
```

```json
{
  "version": 1,
  "workflow": {
    "implementationAgent": "codex",
    "stackPerStory": true
  }
}
```

It is a sidecar rather than a block inside `prd.md` because `prd.md` is
authored and rewritten by the agent; asking it to preserve Loop's bookkeeping
across every edit would be fragile, and losing that block silently would change
how the PRD gets implemented. A PRD without a sidecar is entirely normal —
every PRD written before this existed — and means defaults, not an error.

### Control states

Implementation status and the Start, Pause, Resume and Stop controls are
derived from the authoritative session state, never from the result of the last
request. States are shown as text: `Starting`, `Running`, `Pausing`, `Paused`,
`Resuming`, `Stopping`, `Stopped`, `Failed`.

A failed Start returns the PRD to a startable state and leaves Pause and Stop
disabled. Retrying creates at most one session, and a successful retry clears
the earlier error rather than leaving it on screen next to a running loop.

---

## Driving it headlessly

`cmd/loopctl` exposes the same engine without a window, which is what the
end-to-end tests use:

```bash
loopctl workflow <prd> -json
```

Reports a PRD's saved settings and the agent that would actually implement it,
including the error when a saved agent has been uninstalled.
