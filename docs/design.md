# tmux-todo (`tdo`) — design common ground

Settled 2026-08-19 via grilling session. This is the reference every task in `docs/tasks/`
builds against. If a task contradicts this document, this document is wrong — fix it here
first, then the task.

## What it is

A tmux-native TODO manager with sesh-style ergonomics. A keybind opens a popup where you
create, complete, delete and re-scope tasks. Tasks are scoped so that returning to a
session or a project brings back its pending action items.

- **Repo:** `agusarias/tmux-todo` · **Binary:** `tdo` · **TPM plugin:** yes
- **Language:** Go, single static binary. Cold start must be imperceptible in a popup.
- **Store:** SQLite in the XDG data dir (`~/.local/share/tmux-todo/tasks.db`). Local only.

## Scope model — three independent axes

A task has exactly one scope. The three are orthogonal; none is nested inside another.

| Scope     | Key                                   | Visible when                            |
|-----------|---------------------------------------|-----------------------------------------|
| `global`  | (none)                                | Always                                   |
| `dir`     | git repo root of the active pane's cwd, else the literal path | Active pane is inside that repo/path |
| `session` | tmux session **name** alone           | You are in a session with that name      |

- **Session tasks follow the session across folders.** They are not tied to a directory.
- **Dir tasks follow the directory across sessions.** Two sessions on the same repo share
  the same dir list.
- **Dir key normalization:** resolve the active pane's cwd to its git repo root. Git
  worktrees fold into their **main repo**, so a hotfix or taskflow worktree shares the
  parent project's list. Non-repo paths are used literally (`~/notes` is its own scope).
- **The dir list changes as you `cd`.** This is intended. `cd ~` shows the `~` list.

### Session renames and stale names

- A tmux `session-renamed` hook (installed by the TPM plugin) rewrites the scope key so
  tasks follow the rename. tmux does not tell the hook the *old* name, so this needs a
  durable `session_id -> name` map (v2 `sessions` table): the id survives a rename, the
  name does not. The hook therefore takes **no argument** — see the note under Storage.
- The hook is best-effort. Sessions also die for reasons other than renames, so the
  all-tasks view must still list **non-running session groups** and let you re-home or
  delete them.
- A new session that reuses an old name inherits that name's tasks. Accepted: same name,
  same work.

## Popup UX

Opened via `tmux display-popup -E`, ~60% × 60%, centered, **floored at 60 × 15**.
**Stays open** across actions — toggle several tasks, add two more, delete one, all in one
popup. `Esc` / `q` closes. The only action that closes it is jumping to another session
from the all-tasks view.

The floor is a measured refinement of the percentage, not a contradiction of it: 60% of a
standard 80×24 terminal is 48×14, which leaves the TUI a 46×12 pane — below the 58 columns
the footer needs and near the bottom of the range the frame invariant is asserted over.
60 × 15 gives it 58×13. Above 100 × 25 the percentage governs and the floor never applies.
`display-popup` does not expand formats in `-w`/`-h`, so the keybind picks the size with
`if-shell` rather than an expression; the exact commands are in
`docs/tasks/2026-08-19-tmux-integration-and-rename-hook.md`.

### Default view

All three scopes **merged into one list** with a per-row scope glyph.

```
⌘ rebase onto main           (session: pulsar)
⌘ check CI
· fix auth redirect          (dir: ~/ws/pulsar)
· write migration
◉ call the dentist           (global)

j/k move · space done · ? keys · q quit
```

**Order:** scope tier (session → dir → global), then newest first within each tier.

**The footer is a pointer, not a keymap.** An earlier version of this mock listed every key
— 83 columns, and 93 once `j/k` and the version stamp were added — inside a popup whose own
geometry above gives the content 42. The list would have been silently truncated from the
right, hiding the keys at the end. `?` opens a keymap overlay instead: it replaces the list
body, so it costs no chrome row, and it is where the version lives too.

### Creating and editing

- `a` opens an **inline input row** inside the popup showing the current scope glyph. It is
  a row *in the list*, at the top, not an extra line of chrome — the frame arithmetic is
  where this popup's bugs have lived.
- `Tab` cycles scope before submitting, **through the scopes this context actually has**.
  Outside tmux that is dir → global; with only global it is a no-op. A scope you cannot
  submit to is never offered.
- `Enter` saves, and that scope choice becomes the **sticky default** for the next task. An
  empty `Enter` cancels instead: nothing exists yet, so there is nothing to lose.
- `e` reuses the same input row, in place of the row being edited, to edit its text. Here an
  empty `Enter` is **refused** — the task is on screen and blanking it would destroy
  something visible. `Tab` is inert; scope changes go through `s`.
- `s` cycles an existing task's scope in place, through the same available-only cycle. It
  does **not** move the sticky default: correcting one old row should not redirect the next
  add.

### Completion vs deletion

- `space` **toggles** completion: the row stays visible **struck-through** so the action is
  legible and reversible in the moment. Pressing `space` again undoes it, which is the only
  undo the product has until an undo stack lands.
- Completed rows are **hidden from the view**, never deleted. A row you complete now stays
  on screen for the rest of this popup; one that was already done when the popup opened, or
  completed more than 24h ago, is hidden when you arrive. The row remains in the DB marked
  done — history and stats stay possible later.

  *(Earlier wording said rows were "purged from the view" **and** that they remain in the
  DB. The only store primitive for purging is a hard `DELETE`, so those two halves could not
  both hold; resolved as hide-only. `store.PurgeDone` therefore has no caller and exists for
  an explicit `tdo purge` if one is ever wanted.)*
- `d` removes the row and `u` puts it back. The delete is **queued**, not written: the row
  stays in the database until the popup closes, so `u` restores it with its original id,
  timestamp and position — none of which a re-insert could return. Two consequences, both
  accepted: a queued row is still visible to a concurrent `tdo list`, and a popup that dies
  without a clean exit deletes nothing. Both fail towards keeping your data.

### All-tasks view

`g` toggles a wide view of every task grouped by scope, including scopes that aren't
currently active.

```
─ SESSION pulsar ─ (live)
  [ ] rebase onto main        ↵ switch there
─ SESSION api ─ (not running)
  [ ] fix flaky test          ↵ sesh connect api
─ DIR ~/ws/sesh ─
  [ ] update README
─ GLOBAL ─
  [ ] call the dentist
```

- `Enter` on a session-scoped task **switches to that session** (`tmux switch-client` if
  live, `sesh connect` otherwise) and closes the popup.
- Non-running session groups are labelled as such and support re-home (`r`) and delete.

## CLI surface

The TUI is one consumer of a shared core; everything is reachable non-interactively.

```
tdo add "fix flaky test" [--session|--dir|--global]
tdo list [--scope=session|dir|global|all] [--json]
tdo done <id>
tdo rm <id>
tdo count [--pending]
```

Scope flags default to the same sticky default the TUI uses. Outside tmux the CLI works
fully except that `session` scope is unavailable.

## Storage

SQLite with a `schema_version` table and versioned migrations from day one — growing the
model later is an `ALTER`, not a rewrite.

```sql
-- v1
tasks(id, text, done, done_at, scope_kind, scope_key, created_at)
-- v2
sessions(session_id, name, updated_at)
```

`sessions` is the tmux `session_id -> name` map the rename hook reads. It is written
wherever a session scope is resolved, and a failed write never fails the command: a stale
map costs one unrecovered rename, which the all-tasks view can re-home.

Deliberately **not** in v1: priority, due dates, tags, notes, subtasks. The schema must
tolerate adding them.

## Distribution

TPM plugin wrapping the Go binary:

```tmux
set -g @plugin 'agusarias/tmux-todo'
set -g @todo-key 't'
```

The plugin script installs the popup keybind, installs the `session-renamed` hook, and
resolves the `tdo` binary.

## v1 cut line

**In:** three scopes · merged popup · add/edit/complete/delete/re-scope · all-tasks view
with sesh jump · rename hook · full CLI with `--json` · TPM plugin.

**Deferred:** fuzzy search/filter over task text · tmux statusline pending count · undo
stack · a done/history view · priorities, tags, due dates · multi-machine sync.
