package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/agusarias/tmux-todo/internal/task"
)

func runList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "database path (default: XDG data dir)")
	scopeFlag := fs.String("scope", "", "session|dir|global|all (default: the active merged set)")
	all := fs.Bool("all", false, "include completed tasks")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := parseArgs(fs, args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		return fail(stderr, usagef("list takes no arguments, got %q", fs.Arg(0)))
	}

	e, closeDB, err := openEnv(*dbPath, stdout, stderr)
	if err != nil {
		return fail(stderr, err)
	}
	defer closeDB()

	filter, err := e.filter(*scopeFlag, *all)
	if err != nil {
		return fail(stderr, err)
	}
	// store.List already returns the merged list's order — scope tier (session,
	// dir, global), then newest first — so nothing is re-sorted here.
	tasks, err := e.db.List(context.Background(), filter)
	if err != nil {
		return fail(stderr, err)
	}

	if *asJSON {
		out, err := marshalTasks(tasks)
		if err != nil {
			return fail(stderr, err)
		}
		e.out.Write(out)
		return 0
	}
	writeRows(e.out, tasks)
	return 0
}

// writeRows prints the human-facing table: id, done marker, scope, text.
//
// This output is explicitly *not* a compatibility promise — --json is the
// contract — so it is free to be readable rather than parseable. Columns are
// sized from the rows actually present so short lists do not carry a wide, empty
// scope column.
func writeRows(w io.Writer, tasks []task.Task) {
	labels := make([]string, len(tasks))
	idWidth, scopeWidth := 0, 0
	for i, t := range tasks {
		labels[i] = scopeLabel(t.Scope)
		if n := len(strconv.FormatInt(t.ID, 10)); n > idWidth {
			idWidth = n
		}
		if n := len(labels[i]); n > scopeWidth {
			scopeWidth = n
		}
	}
	for i, t := range tasks {
		marker := "[ ]"
		if t.Done {
			marker = "[x]"
		}
		fmt.Fprintf(w, "%*d %s %-*s  %s\n", idWidth, t.ID, marker, scopeWidth, labels[i], t.Text)
	}
}

// scopeLabel names a scope in one column. Global carries no key, so it is just
// "global"; dir keys are absolute paths, abbreviated at $HOME so the useful tail
// stays on screen.
func scopeLabel(s task.Scope) string {
	if s.Key == "" {
		return string(s.Kind)
	}
	return string(s.Kind) + ":" + abbreviate(s.Key)
}

func abbreviate(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return path
}
