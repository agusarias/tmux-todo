# All-tasks View With Sesh Jump

**Status:** draft
**Worktree:** none

## Goal
`g` toggles a wide view of every task grouped by scope, including non-running session
groups labelled as such. `Enter` on a session-scoped task switches to that session
(`tmux switch-client` when live, `sesh connect` otherwise) and closes the popup. Stale
session groups support re-home and delete.

**Design:** docs/design.md — "All-tasks view"
