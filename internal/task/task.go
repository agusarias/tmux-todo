// Package task holds the core domain types shared by the store, CLI and TUI.
//
// It deliberately contains no persistence or scope-resolution logic: the store
// package owns SQLite, the scope package owns turning a tmux pane into a scope.
package task

import "time"

// ScopeKind is one of the three independent scope axes described in
// docs/design.md. A task has exactly one scope; none is nested in another.
type ScopeKind string

const (
	// ScopeGlobal tasks are visible everywhere and carry no scope key.
	ScopeGlobal ScopeKind = "global"
	// ScopeDir tasks are keyed by git repo root (or literal path) and follow
	// the directory across sessions.
	ScopeDir ScopeKind = "dir"
	// ScopeSession tasks are keyed by tmux session name alone and follow the
	// session across directories.
	ScopeSession ScopeKind = "session"
)

// ScopeKinds lists every valid kind in display order: session, dir, global.
// The order matches the merged popup list's scope tiers.
func ScopeKinds() []ScopeKind {
	return []ScopeKind{ScopeSession, ScopeDir, ScopeGlobal}
}

// Valid reports whether k is one of the three known kinds.
func (k ScopeKind) Valid() bool {
	switch k {
	case ScopeGlobal, ScopeDir, ScopeSession:
		return true
	default:
		return false
	}
}

func (k ScopeKind) String() string { return string(k) }

// Scope is a kind plus its key. Global scopes carry an empty key.
type Scope struct {
	Kind ScopeKind
	Key  string
}

// Task mirrors the v1 schema in docs/design.md:
// tasks(id, text, done, done_at, scope_kind, scope_key, created_at).
type Task struct {
	ID        int64
	Text      string
	Done      bool
	DoneAt    *time.Time
	Scope     Scope
	CreatedAt time.Time
}
