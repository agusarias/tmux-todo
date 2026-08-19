# CLI Surface

**Status:** draft
**Worktree:** none

## Goal
Build the non-interactive surface over the shared core: `tdo add` with scope flags,
`tdo list` with `--scope` and `--json`, `tdo done`, `tdo rm`, `tdo count --pending`.
Scope flags fall back to the same sticky default the TUI uses.

**Design:** docs/design.md — "CLI surface"
