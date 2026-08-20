package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/agusarias/tmux-todo/internal/scope"
	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

// newResolver is the resolution seam. Tests replace it so they depend on
// neither the test process's cwd nor a live tmux server.
//
// It hands back the *Resolver*, not just a Resolved: StickyDefault is a method,
// so the commands need the receiver. It must stay scope.NewResolver rather than
// scope.Resolver{} — the zero value has TmuxEnv == "", which means "not inside
// tmux", so a zero Resolver makes session scope permanently unavailable in the
// shipped binary while every injected test still passes. That bug has shipped
// once already (see internal/scope/scope.go's Resolver doc comment).
var newResolver = scope.NewResolver

// env is everything a command needs that depends on the environment: the open
// database, the scopes available right now, and where to write. Resolved once
// per invocation and threaded into each command so the commands themselves stay
// pure argument-handling.
type env struct {
	db       *store.DB
	resolver scope.Resolver
	scopes   scope.Resolved
	out      io.Writer
	err      io.Writer
}

// openStore resolves --db (empty means the XDG data dir, as doctor does) and
// opens the database, without touching the environment. The returned func closes
// it; it is nil when an error is returned.
//
// Split out of openEnv for the one command that must not resolve: see
// runSessionRenamed.
func openStore(dbFlag string) (*store.DB, func(), error) {
	path := dbFlag
	if path == "" {
		p, err := store.DefaultPath()
		if err != nil {
			return nil, nil, err
		}
		path = p
	}
	db, err := store.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return db, func() { db.Close() }, nil
}

// openEnv opens the store and resolves the current context.
//
// It also refreshes the session_id -> name map, which is why every ordinary
// command goes through here: the map is only useful if it is fresh, and the
// cheapest way to keep it fresh is to write it wherever a session scope was
// resolved anyway. recordSession never fails the command.
func openEnv(dbFlag string, out, errOut io.Writer) (*env, func(), error) {
	db, closeDB, err := openStore(dbFlag)
	if err != nil {
		return nil, nil, err
	}
	resolver := newResolver()
	resolved, err := resolver.Resolve()
	if err != nil {
		closeDB()
		return nil, nil, err
	}
	recordSession(context.Background(), db, resolved)
	e := &env{db: db, resolver: resolver, scopes: resolved, out: out, err: errOut}
	return e, closeDB, nil
}

// scopeAll is the --scope value that escapes the active set: every scope in the
// database, including keys that are not currently resolvable.
const scopeAll = "all"

// filter turns the --scope flag into a store.Filter. It exists exactly once
// because list and count must agree on what --scope means; two copies of this
// defaulting rule would be two chances to disagree.
//
// The three cases are genuinely different and easy to conflate:
//
//	no flag     -> the active merged set, populated *explicitly*
//	--scope=all -> Scopes left nil, which the store reads as every scope
//	--scope=<k> -> that one scope, or ErrUnavailable
//
// An empty Filter.Scopes means "all" to the store, so the flagless case must
// name its scopes rather than leaving the field zero.
func (e *env) filter(scopeFlag string, includeDone bool) (store.Filter, error) {
	f := store.Filter{IncludeDone: includeDone}
	switch {
	case scopeFlag == "":
		f.Scopes = e.scopes.Active()
	case scopeFlag == scopeAll:
		// Deliberately nil.
	default:
		kind := task.ScopeKind(scopeFlag)
		if !kind.Valid() {
			return store.Filter{}, usagef("unknown scope %q (want %s or %s)",
				scopeFlag, strings.Join(kindNames(), ", "), scopeAll)
		}
		// A read of an unavailable scope fails like a write does: an empty list
		// would be indistinguishable from a scope that has no tasks.
		s, err := e.scopes.Lookup(kind)
		if err != nil {
			return store.Filter{}, err
		}
		f.Scopes = []task.Scope{s}
	}
	return f, nil
}

// addScope turns the three mutually exclusive scope flags into the scope a new
// task goes to, falling back to the sticky default when none is given.
//
// The sticky default is only ever *read* here. Writing it from the CLI would let
// a shell alias or cron job running `tdo add --global` silently change what the
// next popup add defaults to — action at a distance the user cannot see. Only
// the TUI's Enter sets it.
func (e *env) addScope(session, dir, global bool) (task.Scope, error) {
	var kinds []task.ScopeKind
	if session {
		kinds = append(kinds, task.ScopeSession)
	}
	if dir {
		kinds = append(kinds, task.ScopeDir)
	}
	if global {
		kinds = append(kinds, task.ScopeGlobal)
	}
	if len(kinds) > 1 {
		return task.Scope{}, usagef("--session, --dir and --global are mutually exclusive")
	}

	kind := e.resolver.StickyDefault(e.scopes)
	if len(kinds) == 1 {
		// An explicit flag is never degraded: --session outside tmux fails
		// rather than filing the task somewhere the user did not ask for.
		kind = kinds[0]
	}
	return e.scopes.Lookup(kind)
}

func kindNames() []string {
	kinds := task.ScopeKinds()
	names := make([]string, 0, len(kinds))
	for _, k := range kinds {
		names = append(names, string(k))
	}
	return names
}

// usageError marks a failure the user fixes by retyping the command, so it exits
// 2 rather than 1. It wraps rather than replaces the message, which keeps the
// sentinel out of what the user reads.
type usageError struct{ err error }

func (u usageError) Error() string { return u.err.Error() }
func (u usageError) Unwrap() error { return u.err }

func usagef(format string, a ...any) error {
	return usageError{fmt.Errorf(format, a...)}
}

// fail reports err and returns the exit code it deserves: 2 for a usage error,
// 1 for anything that went wrong at runtime.
func fail(w io.Writer, err error) int {
	fmt.Fprintf(w, "tdo: %v\n", err)
	var u usageError
	if errors.As(err, &u) {
		return 2
	}
	return 1
}

// parseArgs parses args in a way that does not depend on flag order.
//
// stdlib flag stops at the first non-flag argument, so `tdo add "text" --global`
// would drop --global into fs.Args() and file the task under the sticky default
// while exiting 0 — a wrong-scope task with no error, the worst failure this CLI
// has available. docs/design.md writes the flag *after* the text, so that is the
// order a user will type. Hoisting the flag tokens ahead of the positionals
// makes both orders mean the same thing.
//
// A leading-dash token is still a flag, not text: absorbing it would mean
// `tdo add x -sesion` files a task called "-sesion" and exits 0, which is the
// silent failure the hoist exists to prevent. Users escape with an explicit
// "--", and the "--" this function inserts keeps the escaped text intact
// through the reorder.
func parseArgs(fs *flag.FlagSet, args []string) error {
	flags, positionals := splitArgs(fs, args)
	reordered := make([]string, 0, len(args)+1)
	reordered = append(reordered, flags...)
	if len(positionals) > 0 {
		reordered = append(reordered, "--")
		reordered = append(reordered, positionals...)
	}
	return fs.Parse(reordered)
}

// splitArgs partitions args into flag tokens and positionals.
//
// The subtlety is that a flag's *value* can be a separate token (`--db path`),
// and it must travel with its flag rather than being mistaken for a positional —
// otherwise `tdo add "text" --db path` reorders into `--db "text" path` and the
// database flag quietly takes the task text as its value. Whether the next token
// belongs to the flag is a question only the FlagSet can answer, hence fs.
func splitArgs(fs *flag.FlagSet, args []string) (flags, positionals []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return flags, append(positionals, args[i+1:]...)
		}
		if len(arg) < 2 || arg[0] != '-' {
			positionals = append(positionals, arg)
			continue
		}
		flags = append(flags, arg)
		name := strings.TrimLeft(arg, "-")
		if strings.Contains(name, "=") {
			continue // --flag=value carries its own value
		}
		if takesValue(fs, name) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return flags, positionals
}

// takesValue reports whether the named flag needs the following token as its
// value. Boolean flags never do — `--global text` must leave "text" a
// positional. An unknown name is treated as valueless so fs.Parse is the one
// that reports it, keeping the error message in one place.
func takesValue(fs *flag.FlagSet, name string) bool {
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return !(ok && b.IsBoolFlag())
}
