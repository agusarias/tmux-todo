package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/agusarias/tmux-todo/internal/task"
)

// ErrNoSession reports that nothing has been recorded for a session id. It is
// deliberately its own sentinel rather than ErrNotFound: the rename path has to
// tell "tdo has never run in this session" (a silent no-op — the hook fires on
// every rename in the user's tmux) apart from a database that actually failed.
var ErrNoSession = errors.New("session not recorded")

// RecordSession upserts the tmux session_id -> name mapping.
//
// This is the map the rename hook reads to recover the *old* session name, which
// tmux does not hand to the hook. Callers refresh it whenever they resolve a
// session scope, so the id of every session tdo has run in maps to the name its
// tasks are filed under.
//
// The updated_at stamp comes from DB.now, so tests freeze it like every other
// timestamp in this package.
func (db *DB) RecordSession(ctx context.Context, id, name string) error {
	id, name = strings.TrimSpace(id), strings.TrimSpace(name)
	if id == "" {
		return errors.New("store: empty session id")
	}
	if name == "" {
		return errors.New("store: empty session name")
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO sessions (session_id, name, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (session_id) DO UPDATE SET name = excluded.name, updated_at = excluded.updated_at`,
		id, name, db.now().Unix())
	if err != nil {
		return fmt.Errorf("record session %s: %w", id, err)
	}
	return nil
}

// SessionName returns the name last recorded for a session id, or ErrNoSession
// if the id is unknown.
func (db *DB) SessionName(ctx context.Context, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("store: empty session id")
	}
	var name string
	err := db.QueryRowContext(ctx, `SELECT name FROM sessions WHERE session_id = ?`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("session %s: %w", id, ErrNoSession)
	}
	if err != nil {
		return "", fmt.Errorf("read session %s: %w", id, err)
	}
	return name, nil
}

// RenameSession moves every session-scoped task from oldKey to newKey and
// reports how many rows moved. Rows in dir and global scope are untouched, and
// so are session rows under any other key.
//
// It is one UPDATE on purpose. SQLite wraps a lone statement in its own
// transaction, so the rewrite is already all-or-nothing; a BEGIN around it would
// buy nothing and hold the write lock longer on a path a tmux hook triggers.
//
// Renaming onto a key that already has tasks **merges** them: the rewritten rows
// join the existing ones. That is docs/design.md's accepted behaviour ("a new
// session that reuses an old name inherits that name's tasks"), not an accident
// to guard against — see TestRenameSessionOntoExistingKeyMerges.
func (db *DB) RenameSession(ctx context.Context, oldKey, newKey string) (int, error) {
	oldKey, newKey = strings.TrimSpace(oldKey), strings.TrimSpace(newKey)
	if oldKey == "" {
		return 0, errors.New("store: empty old session key")
	}
	if newKey == "" {
		return 0, errors.New("store: empty new session key")
	}
	if oldKey == newKey {
		return 0, nil
	}
	res, err := db.ExecContext(ctx,
		`UPDATE tasks SET scope_key = ? WHERE scope_kind = ? AND scope_key = ?`,
		newKey, string(task.ScopeSession), oldKey)
	if err != nil {
		return 0, fmt.Errorf("rename session %q -> %q: %w", oldKey, newKey, err)
	}
	moved, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rename session %q -> %q: %w", oldKey, newKey, err)
	}
	return int(moved), nil
}
