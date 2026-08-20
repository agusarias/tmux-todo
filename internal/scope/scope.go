// Package scope turns the current context into the scopes a task can belong to:
// the tmux session name, the git repo root of the active pane's cwd (worktrees
// folding into their main repo), and global.
//
// The strings this package produces become durable database keys, so the
// normalization rules are pinned by tests rather than left to chance: keys are
// absolute, cleaned, symlink-resolved, never case-folded, and never empty for
// session or dir. A scope that cannot be determined is absent, not empty-keyed.
package scope

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/agusarias/tmux-todo/internal/task"
)

// ErrUnavailable reports that a scope kind cannot be used in this context —
// asking for session scope outside tmux, for instance. Callers surface it rather
// than silently substituting another scope, so `tdo add --session` in a plain
// terminal fails loudly instead of filing the task somewhere unexpected.
var ErrUnavailable = errors.New("scope unavailable in this context")

// Resolver reads the environment. Its zero value talks to the real one; tests
// fill the fields in so they need neither a tmux server nor a git checkout.
type Resolver struct {
	// TmuxEnv is the value of $TMUX. Empty means "not inside tmux", which makes
	// session scope unavailable and skips the tmux subprocess entirely.
	TmuxEnv string
	// Run executes a command and returns its stdout. Defaults to exec.Command.
	Run func(name string, args ...string) ([]byte, error)
	// Getwd reports the working directory. Defaults to os.Getwd.
	Getwd func() (string, error)
	// StateDir holds the sticky default. Empty means the XDG lookup.
	StateDir string
}

// Resolved is the set of scopes available right now.
type Resolved struct {
	// Session is nil outside tmux or when the session name cannot be read.
	Session *task.Scope
	// Dir is nil when no directory can be determined at all.
	Dir *task.Scope
	// Global is always present, always empty-keyed.
	Global task.Scope
}

// Resolve resolves the current context against the real environment.
func Resolve() (Resolved, error) { return Resolver{}.Resolve() }

// Resolve reports the scopes available in this context. It returns an error only
// when nothing at all could be determined; an individual scope being
// unavailable is expressed by a nil field, not by an error.
func (r Resolver) Resolve() (Resolved, error) {
	out := Resolved{Global: task.Scope{Kind: task.ScopeGlobal}}

	sessionName, panePath := r.queryTmux()
	if sessionName != "" {
		out.Session = &task.Scope{Kind: task.ScopeSession, Key: sessionName}
	}

	path := panePath
	if path == "" {
		// Not in tmux, or the query failed: fall back to this process's cwd.
		// display-popup is not guaranteed to start in the pane's path, which is
		// why the pane query comes first rather than the other way round.
		if wd, err := r.getwd(); err == nil {
			path = wd
		}
	}
	if path != "" {
		if key, err := DirKey(path); err == nil {
			out.Dir = &task.Scope{Kind: task.ScopeDir, Key: key}
		}
	}
	return out, nil
}

// queryTmux reads the session name and the active pane's path in one
// display-message call — two subprocesses would double the cost on a hot path.
// Any failure yields empty strings; tmux being absent is not an error here.
func (r Resolver) queryTmux() (sessionName, panePath string) {
	if r.TmuxEnv == "" {
		return "", ""
	}
	// A literal newline in the format string, so one call returns two lines.
	const format = "#{session_name}\n#{pane_current_path}"
	out, err := r.run("tmux", "display-message", "-p", format)
	if err != nil {
		return "", ""
	}
	lines := strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	if len(lines) > 0 {
		sessionName = strings.TrimSpace(lines[0])
	}
	if len(lines) > 1 {
		panePath = strings.TrimSpace(lines[1])
	}
	return sessionName, panePath
}

func (r Resolver) run(name string, args ...string) ([]byte, error) {
	if r.Run != nil {
		return r.Run(name, args...)
	}
	return exec.Command(name, args...).Output()
}

func (r Resolver) getwd() (string, error) {
	if r.Getwd != nil {
		return r.Getwd()
	}
	return os.Getwd()
}

// Active returns the usable scopes in the merged list's tier order: session,
// then dir, then global. Absent scopes are skipped.
//
// The order lives here rather than in each consumer because the popup list, the
// all-tasks view and store.List all need the same one.
func (rs Resolved) Active() []task.Scope {
	out := make([]task.Scope, 0, 3)
	if rs.Session != nil {
		out = append(out, *rs.Session)
	}
	if rs.Dir != nil {
		out = append(out, *rs.Dir)
	}
	return append(out, rs.Global)
}

// Lookup returns the scope for one kind, or ErrUnavailable if this context has
// no such scope.
func (rs Resolved) Lookup(kind task.ScopeKind) (task.Scope, error) {
	switch kind {
	case task.ScopeGlobal:
		return rs.Global, nil
	case task.ScopeSession:
		if rs.Session == nil {
			return task.Scope{}, fmt.Errorf("session scope: %w (not inside tmux)", ErrUnavailable)
		}
		return *rs.Session, nil
	case task.ScopeDir:
		if rs.Dir == nil {
			return task.Scope{}, fmt.Errorf("dir scope: %w (no working directory)", ErrUnavailable)
		}
		return *rs.Dir, nil
	default:
		return task.Scope{}, fmt.Errorf("unknown scope kind %q", kind)
	}
}

// Has reports whether a kind is usable in this context.
func (rs Resolved) Has(kind task.ScopeKind) bool {
	_, err := rs.Lookup(kind)
	return err == nil
}
