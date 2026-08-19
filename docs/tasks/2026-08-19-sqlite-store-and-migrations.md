# SQLite Store And Migrations

**Status:** draft
**Worktree:** none

## Goal
Implement the SQLite store at `~/.local/share/tmux-todo/tasks.db` with the v1 `tasks`
schema, a `schema_version` table, and versioned migrations from day one so later fields
(priority, tags, due dates) are an ALTER rather than a rewrite. Handle concurrent access
from multiple open popups.

**Design:** docs/design.md — "Storage"
