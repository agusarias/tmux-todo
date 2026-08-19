// Package scope turns the active tmux pane into the scope a task belongs to:
// the session name, the git repo root of the pane's cwd (worktrees folding into
// their main repo), or global.
//
// Resolution is deliberately unimplemented here — it is owned by the
// scope-resolution task. This file exists so the package boundary is fixed
// before the store, CLI and TUI tasks start importing it.
package scope
