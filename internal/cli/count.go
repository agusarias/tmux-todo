package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
)

// runCount prints a bare integer. It is the one plausibly hot path — a tmux
// statusline can call it on every redraw — so it counts in SQL rather than
// listing rows and measuring the slice.
func runCount(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("count", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "database path (default: XDG data dir)")
	// See runList: registered for the FlagSet's benefit, read back by newSelector.
	fs.String("scope", "", "session|dir|global|all (default: the active merged set)")
	fs.String("session", "", "tasks filed under this session name, running or not")
	fs.String("dir", "", "tasks filed under this directory's repo root")
	pending := fs.Bool("pending", false, "count only pending tasks")
	if err := parseArgs(fs, args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		return fail(stderr, usagef("count takes no arguments, got %q", fs.Arg(0)))
	}

	e, closeDB, err := openEnv(*dbPath, stdout, stderr)
	if err != nil {
		return fail(stderr, err)
	}
	defer closeDB()

	// Note the polarity: count defaults to the *total* and --pending narrows it,
	// where list defaults to pending and --all widens it. That asymmetry is what
	// docs/design.md specifies, and it is why both go through the same filter()
	// rather than each deciding what IncludeDone means.
	filter, err := e.filter(newSelector(fs), !*pending)
	if err != nil {
		return fail(stderr, err)
	}
	n, err := e.db.Count(context.Background(), filter)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(e.out, n)
	return 0
}
