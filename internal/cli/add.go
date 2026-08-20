package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
)

func runAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "database path (default: XDG data dir)")
	session := fs.Bool("session", false, "scope the task to this tmux session")
	dir := fs.Bool("dir", false, "scope the task to this directory's repo root")
	global := fs.Bool("global", false, "scope the task globally")
	// parseArgs, not fs.Parse: docs/design.md writes the scope flag after the
	// text, and stdlib flag would drop a trailing flag into fs.Args() and file
	// the task under the sticky default while exiting 0.
	if err := parseArgs(fs, args); err != nil {
		return 2
	}

	// Exactly one positional. A silent join would make `tdo add fix flaky test`
	// mean something, and then a mistyped flag (`-sesion`) would be absorbed into
	// the task text instead of being caught.
	switch fs.NArg() {
	case 1:
	case 0:
		return fail(stderr, usagef(`add needs the task text: tdo add "fix flaky test" [--session|--dir|--global]`))
	default:
		return fail(stderr, usagef("add takes exactly one quoted argument, got %d — quote the text", fs.NArg()))
	}

	e, closeDB, err := openEnv(*dbPath, stdout, stderr)
	if err != nil {
		return fail(stderr, err)
	}
	defer closeDB()

	sc, err := e.addScope(*session, *dir, *global)
	if err != nil {
		return fail(stderr, err)
	}
	t, err := e.db.Add(context.Background(), fs.Arg(0), sc)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(e.out, t.ID)
	return 0
}
