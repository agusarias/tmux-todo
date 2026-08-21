package scope

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agusarias/tmux-todo/internal/task"
)

// AppDir is the directory name used under the XDG state dir.
const AppDir = "tmux-todo"

// stickyFile holds one scope kind — never a key. A key would go stale the moment
// you cd or rename a session; the kind is stable and the key is re-resolved at
// use time.
const stickyFile = "default-scope"

// stickyViewFile holds which view the popup opens in: the single word "all" for
// the all-tasks view, anything else (including absent) for the merged list.
//
// A separate file rather than a second word inside default-scope. The two
// preferences are read by different packages for different reasons, and sharing
// a file would mean a parse change to one could eat the other — which is the
// failure this being in the *state* dir makes cheap to shrug off but not cheap
// to notice. See TestStickyViewAndScopeAreIndependent.
const stickyViewFile = "default-view"

// stickyViewAll is the only value that means anything in stickyViewFile.
const stickyViewAll = "all"

// StickyAllTasks reports whether the popup should open in the all-tasks view.
//
// Missing, empty, or holding junk all mean "no preference recorded", which is
// the merged list — silently, and for the same reason storedStickyDefault
// shrugs: a corrupt one-word file must never stop the popup from opening. There
// is deliberately no error return; the caller has nothing useful to do with one.
func (r Resolver) StickyAllTasks() bool {
	dir, err := r.stateDir()
	if err != nil {
		return false
	}
	body, err := os.ReadFile(filepath.Join(dir, stickyViewFile))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(body)) == stickyViewAll
}

// SetStickyAllTasks persists which view the popup was in when it closed.
//
// false writes the file rather than removing it: "explicitly the merged list"
// and "never expressed a preference" behave identically on read, so removal
// would work too — but a write keeps this a single code path, and leaves the
// file's mtime telling the truth about when the choice was last made.
func (r Resolver) SetStickyAllTasks(all bool) error {
	value := ""
	if all {
		value = stickyViewAll
	}
	dir, err := r.stateDir()
	if err != nil {
		return err
	}
	if err := writeStateFile(dir, stickyViewFile, value); err != nil {
		return fmt.Errorf("write sticky view: %w", err)
	}
	return nil
}

// StickyDefault returns the scope kind a new task should default to, degraded to
// something that actually exists in this context.
//
// A stored `session` default in a plain terminal becomes `dir`, and `dir` with no
// resolvable directory becomes `global`. With nothing stored, the default is
// `session` inside tmux and `dir` outside it.
func (r Resolver) StickyDefault(rs Resolved) task.ScopeKind {
	kind, ok := r.storedStickyDefault()
	if !ok {
		kind = task.ScopeDir
		if rs.Session != nil {
			kind = task.ScopeSession
		}
	}
	return degrade(kind, rs)
}

// degrade walks down the tiers until it finds an available one. Global is always
// available, so this terminates.
func degrade(kind task.ScopeKind, rs Resolved) task.ScopeKind {
	for _, candidate := range []task.ScopeKind{kind, task.ScopeDir, task.ScopeGlobal} {
		if rs.Has(candidate) {
			return candidate
		}
	}
	return task.ScopeGlobal
}

// storedStickyDefault reads the persisted kind. A missing, empty, or unparseable
// file is not an error — it just means "no preference recorded", because a
// corrupt one-word file should never stop the popup from opening.
func (r Resolver) storedStickyDefault() (task.ScopeKind, bool) {
	dir, err := r.stateDir()
	if err != nil {
		return "", false
	}
	body, err := os.ReadFile(filepath.Join(dir, stickyFile))
	if err != nil {
		return "", false
	}
	kind := task.ScopeKind(strings.TrimSpace(string(body)))
	if !kind.Valid() {
		return "", false
	}
	return kind, true
}

// SetStickyDefault persists the kind, replacing any previous value. The write is
// atomic — temp file plus rename — so a crash mid-write cannot leave a truncated
// file behind.
func (r Resolver) SetStickyDefault(kind task.ScopeKind) error {
	if !kind.Valid() {
		return fmt.Errorf("sticky default: unknown scope kind %q", kind)
	}
	dir, err := r.stateDir()
	if err != nil {
		return err
	}
	if err := writeStateFile(dir, stickyFile, string(kind)); err != nil {
		return fmt.Errorf("write sticky default: %w", err)
	}
	return nil
}

// writeStateFile replaces one one-word preference file, atomically: temp file
// plus rename, so a crash mid-write cannot leave a truncated file behind.
//
// Extracted when the second preference arrived rather than copied. Both files
// live in the same directory and are read by code that treats a malformed one as
// absent, so "the write is atomic" has to be a property of the mechanism and not
// of each caller remembering to do it the same way.
func writeStateFile(dir, name, value string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, name+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.WriteString(value + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, name))
}

// stateDir is $XDG_STATE_HOME/tmux-todo, falling back to
// ~/.local/state/tmux-todo. The state dir rather than the data dir: this is a
// preference the user can lose without losing anything of theirs.
func (r Resolver) stateDir() (string, error) {
	if r.StateDir != "" {
		return r.StateDir, nil
	}
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, AppDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "state", AppDir), nil
}
