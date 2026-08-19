# Tmux Integration And Rename Hook

**Status:** draft
**Worktree:** none

## Goal
Wire `tdo` into tmux: the popup keybind (`display-popup -E`, ~60%x60%, centered,
configurable via `@todo-key`) and a `session-renamed` hook that rewrites the session scope
key so tasks follow a rename. The hook is best-effort — stale session scopes must still be
reachable from the all-tasks view.

**Design:** docs/design.md — "Session renames and stale names"
