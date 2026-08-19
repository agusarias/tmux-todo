# Scope Resolution

**Status:** draft
**Worktree:** none

## Goal
Resolve the three independent scopes for the current context: `global`, `dir` (active
pane's cwd normalized to its git repo root, with git worktrees folding into the main repo,
literal path when not a repo), and `session` (tmux session name alone). Must degrade
gracefully outside tmux, where session scope is unavailable.

**Design:** docs/design.md — "Scope model"
