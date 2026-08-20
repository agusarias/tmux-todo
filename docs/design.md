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
  tasks follow the rename.
- The hook is best-effort. Sessions also die for reasons other than renames, so the
  all-tasks view must still list **non-running session groups** and let you re-home or
  delete them.
- A new session that reuses an old name inherits that name's tasks. Accepted: same name,
  same work.

## Popup UX

Opened via `tmux display-popup -E`, ~60% × 60%, centered. **Stays open** across actions —
toggle several tasks, add two more, delete one, all in one popup. `Esc` / `q` closes.
The only action that closes it is jumping to another session from the all-tasks view.

### Default view

All three scopes **merged into one list** with a per-row scope glyph.

```
⌘ rebase onto main           (session: pulsar)
⌘ check CI
· fix auth redirect          (dir: ~/ws/pulsar)
· write migration
◉ call the dentist           (global)

1/2/3 filter · a add · e edit · space done · d delete · s re-scope · g all · q quit
```

**Order:** scope tier (session → dir → global), then newest first within each tier.

### Creating and editing

- `a` opens an **inline input row** inside the popup showing the current scope glyph.
- `Tab` cycles scope (session / dir / global) before submitting.
- `Enter` saves, and that scope choice becomes the **sticky default** for the next task.
- `e` reuses the same input row to edit an existing task's text.
- `s` cycles an existing task's scope in place.

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
- `d` deletes outright.

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
```

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
