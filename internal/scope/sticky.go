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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, stickyFile+".*")
	if err != nil {
		return fmt.Errorf("write sticky default: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.WriteString(string(kind) + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("write sticky default: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write sticky default: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, stickyFile)); err != nil {
		return fmt.Errorf("write sticky default: %w", err)
	}
	return nil
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
