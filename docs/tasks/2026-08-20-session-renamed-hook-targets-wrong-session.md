# `session-renamed` resolves the wrong session, so the hook never re-files anything

**Status:** draft
**Worktree:** none

## Goal
Renaming a tmux session moves that session's tasks to the new name, when the
rename happens through the installed `session-renamed` hook — not only when
`tdo session-renamed` is typed in a pane.

## Why
The whole `sessions` id -> name map exists to survive renames, and it currently
buys nothing in the one path that matters: the hook. Found while installing the
plugin on the developer's real tmux server (2026-08-20) — the popup keybind
works, this half does not.

## Diagnosis (measured, tmux 3.7b)
The hook body is `run-shell -b 'tdo session-renamed'`, and the design rests on
the claim that the child inherits `$TMUX` whose third field is the session the
hook fired for, so asking tmux is enough. The inheritance is real. The
conclusion is not: **tmux resolves "current session" from the client, and a
`run-shell` child has no client**, so an untargeted query falls back to an
unrelated session.

A canary hook (a script, so no `#{...}` reaches the server for pre-expansion)
on a rename of session `$100`:

```
child TMUX=/private/tmp/tmux-501/default,37572,100
child sees name=todo          <- untargeted: a DIFFERENT session
child sees id=$24
```

and with the fix under test in the same hook:

```
TMUX third field -> target $100
untargeted  name=todo
targeted    name=tdo-probe-F6   <- correct, and the post-rename name
```

So `tdo session-renamed` reads some other session's name, compares it to that
session's map entry, finds no rename, and **exits 0 having done nothing**. It is
silent in both channels.

Two traps this hid behind:
1. **It works when typed in a pane**, because a pane has a client. Every manual
   check of this command passes while the hook is broken.
2. A `run-shell` canary containing `#{session_id}` reports the *server's* view,
   not the child's — tmux expands formats in the `run-shell` argument before
   `sh` sees it (`$100` then reads as `${1}00`, printing `00`). A canary written
   the obvious way agrees with the broken assumption.

## Constraints
- Keep the hook body free of interpolated session **names**: a name is user data
  and no tmux format escapes for a shell (the injection note in CLAUDE.md), and
  a name containing `:` cannot be a tmux target at all. The session **id** from
  `$TMUX` is `$<number>` and is safe to interpolate.
- The fix belongs on the tdo side, not in the hook string — the installed hook
  should stay the bare `run-shell -b '<tdo> session-renamed'` so already-installed
  hooks keep working after an upgrade.
- `internal/store` and `internal/tui` stay environment-blind; this is
  `internal/scope` / `internal/cli` work.

## Critical surface
None by the usual list — no migration, no auth, no prod data. But it does move
rows between scope keys, and scope keys are durable database keys, so a fix that
targets the *wrong* session would re-home someone else's tasks. Worth treating
the re-home path as higher-risk than its size suggests.

## Definition of done
1. `tdo session-renamed` asks tmux about the session named by `$TMUX`'s third
   field, by target, rather than relying on tmux's client-derived current session.
2. Renaming a session via the tmux hook moves that session's tasks to the new
   name.
3. A regression test that fails against today's code. It must exercise the
   *clientless* path — a test that runs the command with a client present passes
   against the bug (see trap 1 above), which makes it worthless.
4. No session name is interpolated into any tmux command or hook body.
5. CLAUDE.md's `session-renamed` note is corrected (already done in the commit
   that filed this task) and the residual claim in `docs/design.md`, if any, is
   brought in line.

## Verification
- `make test` for the unit level, `make test-plugin` for the hook install.
- End to end on a real server: add a session task, rename the session with
  `tmux rename-session` **from inside a pane of it** (so the hook fires), and
  assert the task's scope key followed. Then repeat with the rename issued from
  outside tmux entirely, which is the clientless case.

## Decisions
- 2026-08-20: diagnosed during the plugin install; filed as a draft rather than
  fixed inline, because the fix lands in the scope-resolution seam that CLAUDE.md
  records as having shipped two vacuous-test bugs already, and DoD 3 needs
  thought about how to make the clientless path testable.

## Plan
(Checkpoint 1 pending.)

## Evidence
(Executor fills this in.)
