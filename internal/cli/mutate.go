package cli

import (
	"context"
	"flag"
	"io"
	"strconv"
)

// runDone and runRm both mutate the user's real database from a one-line
// command, so the id handling and the not-found path are the parts that matter:
// exiting 0 on a no-op would tell the user something happened when nothing did.
func runDone(args []string, stdout, stderr io.Writer) int {
	return runByID("done", args, stdout, stderr,
		func(e *env, id int64) error { return e.db.Complete(context.Background(), id) })
}

func runRm(args []string, stdout, stderr io.Writer) int {
	return runByID("rm", args, stdout, stderr,
		func(e *env, id int64) error { return e.db.Delete(context.Background(), id) })
}

// runByID is the shared shape of the two single-id mutations. The store already
// wraps store.ErrNotFound with the verb and the id — "delete task 7: task not
// found" — so the message a user sees needs no assembling here.
func runByID(name string, args []string, stdout, stderr io.Writer, apply func(*env, int64) error) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "database path (default: XDG data dir)")
	if err := parseArgs(fs, args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		return fail(stderr, usagef("%s needs exactly one task id: tdo %s <id>", name, name))
	}
	id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if err != nil {
		return fail(stderr, usagef("%q is not a task id", fs.Arg(0)))
	}

	e, closeDB, err := openEnv(*dbPath, stdout, stderr)
	if err != nil {
		return fail(stderr, err)
	}
	defer closeDB()

	if err := apply(e, id); err != nil {
		return fail(stderr, err)
	}
	return 0
}
