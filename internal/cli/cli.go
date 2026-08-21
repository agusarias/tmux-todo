// Package cli dispatches tdo's subcommands. Parsing is stdlib flag with manual
// dispatch — a handful of commands does not justify a framework, and the popup
// is a hot path where every init counts.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/tui"
)

// Version is stamped at build time with -ldflags. "dev" when built by hand.
var Version = "dev"

const usage = `tdo — tmux-native TODO manager

usage:
  tdo [--version] <command> [flags]

commands:
  add "text" [--session|--dir|--global]   add a task (default: sticky scope)
  list [--scope=session|dir|global|all | --session <name> | --dir <path>]
       [--all] [--json]                   list tasks (default: the active set)
  done <id>                               mark a task complete
  rm <id>                                 delete a task
  count [--pending] [--scope=... | --session <name> | --dir <path>]
                                          print a bare count
  tui                                     open the popup TUI
  session-renamed [-- "<name>"]           re-file a renamed session's tasks
                                          under its current name (the tmux
                                          session-renamed hook; no argument
                                          means the session tdo is running in)
  doctor                                  check the toolchain and database wiring
  version                                 print the version
  help                                    print this message

Every command accepts --db <path>. Exit codes: 0 ok, 1 error, 2 usage.
Scope flags may come before or after the text. Task text that starts with a
dash needs a "--" separator: tdo add --global -- "-n is not a flag".
Outside tmux, session scope is unavailable and --session fails rather than
filing the task somewhere else.

Asking about a named scope, on list and count:
  --session <name>  tasks filed under that tmux session name, used verbatim.
  --dir <path>      tasks filed under that directory, normalised to its repo
                    root the same way "add --dir" files them.
Unlike --scope, these ask about stored tasks rather than about where you are:
they never consult tmux, work outside it, and a session that is not running or
a directory you are not in returns an empty list and exit 0. --scope, --session
and --dir are mutually exclusive.

A value starting with a dash must use the "=" form, or it is rejected rather
than swallowed: tdo list --session=-json, not tdo list --session -json.

Limitation: --dir cannot resolve symlinks for a path that no longer exists, so
a deleted directory whose key was stored through a symlink will not match. Use
--scope=all to find stranded dir keys.
`

// Run dispatches args (os.Args[1:]) and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}

	switch args[0] {
	case "--version", "-version", "version":
		fmt.Fprintln(stdout, Version)
		return 0
	case "--help", "-h", "help":
		fmt.Fprint(stdout, usage)
		return 0
	case "add":
		return runAdd(args[1:], stdout, stderr)
	case "list":
		return runList(args[1:], stdout, stderr)
	case "done":
		return runDone(args[1:], stdout, stderr)
	case "rm":
		return runRm(args[1:], stdout, stderr)
	case "count":
		return runCount(args[1:], stdout, stderr)
	case "tui":
		return runTUI(args[1:], stderr)
	case "session-renamed":
		return runSessionRenamed(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "tdo: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// runTUIProgram is the seam that starts the popup. It exists for exactly one
// reason: a test must be able to cover runTUI's assembly without a Bubble Tea
// program actually starting.
//
// The obvious assumption — that a `go test` process has no terminal, so tui.Run
// fails harmlessly at the last step — is **false inside tmux**, which for a tmux
// plugin is where every developer runs the suite. Bubble Tea opens /dev/tty
// directly, so redirecting the test's stdout hides nothing from it: the popup
// really renders, into the developer's pane, and then blocks forever on a
// keystroke that never comes. `go test ./...` prints package results in
// command-line order, so one hung package means `make test` never finishes.
//
// Same shape as newResolver, and for a related reason: a package that reaches
// for the environment needs one substitutable point where it does so, or its
// tests either lie about the environment or depend on it.
var runTUIProgram = tui.Run

// runTUI opens the popup. Everything environment-dependent is resolved here and
// injected: internal/tui never asks tmux or the filesystem anything, which is
// what keeps its Update/View testable headlessly.
//
// It goes through openEnv like every other command — deliberately, because
// openEnv is where the session_id -> name map is refreshed and the popup is the
// invocation a user makes most. There is still no --db flag (docs/design.md), so
// the path is always the XDG one.
func runTUI(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// io.Discard: the popup owns the terminal, so a command-style stdout write
	// would land in the middle of the frame.
	e, closeDB, err := openEnv("", io.Discard, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "tdo: %v\n", err)
		return 1
	}
	defer closeDB()

	// Active() is already in the merged list's tier order (session, dir,
	// global), skipping whatever this context has no scope for.
	//
	// DefaultScope and SetSticky are the two halves of the sticky default, and
	// both come off the *same* resolver openEnv already built. internal/tui must
	// not import internal/scope, and a second `scope.Resolver{}` here would
	// reintroduce the tmux-blindness bug the zero value causes — there is no
	// second construction site to get wrong.
	cfg := tui.Config{
		DB:           e.db,
		Scopes:       e.scopes.Active(),
		Home:         homeDir(),
		Version:      Version,
		DefaultScope: e.resolver.StickyDefault(e.scopes),
		SetSticky:    e.resolver.SetStickyDefault,
		// Resolved once, here, for the same reason as everything else in this
		// struct: internal/tui must not ask tmux anything, least of all from
		// inside a keypress. This is a second subprocess on popup open (~5ms
		// alongside Resolve's display-message), which is the price of the
		// all-tasks view's liveness label being right when the popup opens.
		LiveSessions: e.resolver.LiveSessions(),
		// The key that opened the popup, so pressing it again closes it. The
		// plugin puts @todo-key in the environment (display-popup -e) rather than
		// tdo asking tmux, which would be a second round-trip on the hot path and
		// would still be wrong for a hand-written bind. Unset — a hand-run
		// `tdo tui`, or a prefix-table install where the chord cannot reach the
		// popup — translates to "", meaning no close key.
		CloseKey: popupKey(os.Getenv(popupKeyEnv)),
	}
	jump, err := runTUIProgram(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "tdo: %v\n", err)
		return 1
	}
	// After Run, never alongside it: the popup's queued deletes commit inside
	// its own event loop, so by the time we are here there is nothing a jump can
	// discard. See jump.go for the invocation table.
	if err := performJump(jump, e.resolver.TmuxEnv != "", nil); err != nil {
		fmt.Fprintf(stderr, "tdo: %v\n", err)
		return 1
	}
	return 0
}

// homeDir is used only to abbreviate dir scope keys for display, so failing to
// find it degrades to showing absolute paths rather than to an error.
func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// runDoctor exercises the whole stack end to end: it resolves the data dir,
// opens a real SQLite database with the pure-Go driver and reads the schema
// version back. It is what makes the CGO_ENABLED=0 claim testable from the
// shipped binary rather than only from the test binary.
func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "database path (default: XDG data dir)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	path := *dbPath
	if path == "" {
		p, err := store.DefaultPath()
		if err != nil {
			fmt.Fprintf(stderr, "tdo: %v\n", err)
			return 1
		}
		path = p
	}

	db, err := store.Open(path)
	if err != nil {
		fmt.Fprintf(stderr, "tdo: %v\n", err)
		return 1
	}
	defer db.Close()

	ctx := context.Background()

	version, err := db.Version(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "tdo: %v\n", err)
		return 1
	}
	want, err := store.SchemaVersion()
	if err != nil {
		fmt.Fprintf(stderr, "tdo: %v\n", err)
		return 1
	}
	journal, err := db.JournalMode(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "tdo: %v\n", err)
		return 1
	}
	pending, err := db.Count(ctx, store.Filter{})
	if err != nil {
		fmt.Fprintf(stderr, "tdo: %v\n", err)
		return 1
	}
	total, err := db.Count(ctx, store.Filter{IncludeDone: true})
	if err != nil {
		fmt.Fprintf(stderr, "tdo: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "tdo      %s\n", Version)
	fmt.Fprintf(stdout, "runtime  %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(stdout, "database %s\n", db.Path())
	fmt.Fprintf(stdout, "schema   %d (latest %d)\n", version, want)
	fmt.Fprintf(stdout, "journal  %s\n", journal)
	fmt.Fprintf(stdout, "tasks    %d pending, %d total\n", pending, total)
	if version != want {
		fmt.Fprintf(stderr, "tdo: database is at schema %d, want %d\n", version, want)
		return 1
	}
	fmt.Fprintln(stdout, "ok")
	return 0
}
