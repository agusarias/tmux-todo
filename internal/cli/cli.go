// Package cli dispatches tdo's subcommands. Parsing is stdlib flag with manual
// dispatch — a handful of commands does not justify a framework, and the popup
// is a hot path where every init counts.
package cli

import (
	"flag"
	"fmt"
	"io"
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
  tui       open the popup TUI
  doctor    check the toolchain and database wiring
  version   print the version
  help      print this message
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
	case "tui":
		return runTUI(args[1:], stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "tdo: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func runTUI(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := tui.Run(Version); err != nil {
		fmt.Fprintf(stderr, "tdo: %v\n", err)
		return 1
	}
	return 0
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

	version, err := db.Version()
	if err != nil {
		fmt.Fprintf(stderr, "tdo: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "tdo      %s\n", Version)
	fmt.Fprintf(stdout, "runtime  %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(stdout, "database %s\n", db.Path())
	fmt.Fprintf(stdout, "schema   %d\n", version)
	fmt.Fprintln(stdout, "ok")
	return 0
}
