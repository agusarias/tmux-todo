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

// Resolver reads the environment. Tests fill the fields in so they need neither
// a tmux server nor a git checkout.
//
// Use NewResolver to talk to the real environment — **not** the zero value.
// Run and Getwd default to the real thing when nil, but TmuxEnv cannot: "" is a
// meaningful value there, meaning "not inside tmux", so a zero Resolver reports
// session scope as permanently unavailable. That asymmetry shipped as a bug
// once (see the brief for docs/tasks/2026-08-19-scope-resolution.md): every test
// injected TmuxEnv by hand, so the whole suite passed while the shipped binary
// could never see a tmux session.
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
	// SessionID is tmux's id for that session ("$3"), empty when there is none.
	//
	// It is not a scope key and must never become one: ids reset when the tmux
	// server restarts, so filing tasks under an id would orphan every
	// session-scoped task on reboot. It is here because the id *survives a
	// rename* while the name — the actual key — does not, which is what lets the
	// session-renamed hook recover the old key from the store's id -> name map.
	SessionID string
	// Dir is nil when no directory can be determined at all.
	Dir *task.Scope
	// Global is always present, always empty-keyed.
	Global task.Scope
}

// NewResolver returns a Resolver wired to the real environment.
//
// Callers that need the receiver — StickyDefault and SetStickyDefault are
// methods — must build one with this rather than with Resolver{}, or session
// scope silently disappears for them too.
func NewResolver() Resolver {
	return Resolver{TmuxEnv: os.Getenv("TMUX")}
}

// Resolve resolves the current context against the real environment.
func Resolve() (Resolved, error) { return NewResolver().Resolve() }

// Resolve reports the scopes available in this context. It returns an error only
// when nothing at all could be determined; an individual scope being
// unavailable is expressed by a nil field, not by an error.
func (r Resolver) Resolve() (Resolved, error) {
	out := Resolved{Global: task.Scope{Kind: task.ScopeGlobal}}

	sessionName, panePath, sessionID := r.queryTmux()
	if sessionName != "" {
		out.Session = &task.Scope{Kind: task.ScopeSession, Key: sessionName}
	}
	out.SessionID = sessionID

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

// queryTmux reads the session name, the active pane's path and the session id in
// one display-message call — a second subprocess would double the cost on a hot
// path, which is why the id is a third line of this format string rather than
// its own query. Any failure yields empty strings; tmux being absent is not an
// error here.
func (r Resolver) queryTmux() (sessionName, panePath, sessionID string) {
	if r.TmuxEnv == "" {
		return "", "", ""
	}
	// Literal newlines in the format string, so one call returns three lines.
	const format = "#{session_name}\n#{pane_current_path}\n#{session_id}"
	out, err := r.run("tmux", "display-message", "-p", format)
	if err != nil {
		return "", "", ""
	}
	lines := strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	if len(lines) > 0 {
		sessionName = strings.TrimSpace(lines[0])
	}
	if len(lines) > 1 {
		panePath = strings.TrimSpace(lines[1])
	}
	if len(lines) > 2 {
		sessionID = strings.TrimSpace(lines[2])
	}
	return sessionName, panePath, sessionID
}

// SessionID asks tmux for the id of the session called name.
//
// This is the rename hook's half of the id -> name map. The hook cannot pass the
// id itself: #{session_id} expands to "$0", run-shell hands its argument to sh,
// and sh expands $0 to its own name — so the hook passes the new *name* and the
// binary looks the id up here. One subprocess, like Resolve's.
//
// The target is "=<name>:" — verified against tmux 3.7b, and neither half is
// decoration. The "=" forces an exact name match, so a rename of "dev" cannot be
// answered by a session called "dev-2" (tmux otherwise falls back to prefix and
// then fnmatch matching). The trailing ":" is what makes the "=" work at all:
// display-message takes a target-*pane*, so a bare "=dev" is parsed as a pane
// name and resolves to nothing — and tmux reports that by printing an empty line
// and exiting 0, not by failing. Hence the empty-output check below.
//
// A session name containing ":" cannot be targeted this way, because that is the
// character tmux splits on. Such a rename is simply not recovered; the all-tasks
// view's re-home is the fallback docs/design.md already provides.
func (r Resolver) SessionID(name string) (string, error) {
	if name == "" {
		return "", errors.New("scope: empty session name")
	}
	if r.TmuxEnv == "" {
		return "", fmt.Errorf("session id for %q: %w (not inside tmux)", name, ErrUnavailable)
	}
	out, err := r.run("tmux", "display-message", "-t", "="+name+":", "-p", "#{session_id}")
	if err != nil {
		return "", fmt.Errorf("session id for %q: %w", name, err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return "", fmt.Errorf("session id for %q: tmux reported no id (no such session, or a name tmux cannot target)", name)
	}
	return id, nil
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

// LiveSessions is the set of tmux session names running right now.
//
// It is the all-tasks view's liveness label, resolved once per popup rather than
// per keypress: a subprocess on a keypress inside the popup would put ~5ms and a
// failure mode on the hot path, and the label is only ever used to pick which
// jump command to run — a stale one costs nothing, because a "not running"
// session that turns out to be running is switched to anyway.
//
// It deliberately does *not* short circuit on TmuxEnv the way queryTmux does.
// `tdo tui` can be run from a plain shell with a tmux server up, and in that
// context the sessions are still there to jump to; asking is what makes the
// outside-tmux jump possible at all. tmux being absent, or no server running, is
// not an error here — there is simply nothing live, which is the honest answer.
//
// The names are keys, not display strings: no trimming beyond the line split,
// no case folding. A session name can contain spaces, which is exactly why the
// format is one name per line rather than a separated list.
func (r Resolver) LiveSessions() map[string]bool {
	out, err := r.run("tmux", "list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil
	}
	live := map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		if line != "" {
			live[line] = true
		}
	}
	if len(live) == 0 {
		return nil
	}
	return live
}
