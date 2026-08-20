package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agusarias/tmux-todo/internal/task"
)

// taskColumns is the v1 select list, in Task field order.
const taskColumns = `id, text, done, done_at, scope_kind, scope_key, created_at`

// tierOrder ranks the three scopes the way the merged popup list shows them:
// session first, then dir, then global.
const tierOrder = `CASE scope_kind WHEN 'session' THEN 0 WHEN 'dir' THEN 1 ELSE 2 END`

// listOrder is the merged list's order: scope tier, then newest first. The id
// tiebreak keeps rows created in the same second stable.
const listOrder = tierOrder + `, created_at DESC, id DESC`

// groupedOrder keeps whole scopes together for the all-tasks view.
const groupedOrder = tierOrder + `, scope_key, created_at DESC, id DESC`

// DoneRetention is how long a completed task stays visible after it is
// completed. It is the *view*'s retention, not the store's: nothing is ever
// deleted on this schedule. Defined here so `cli` and `tui` share one number
// rather than each spelling out 24h.
const DoneRetention = 24 * time.Hour

// Filter selects which tasks a query returns.
//
// An empty Scopes slice means every scope in the database, including scope keys
// that are not currently active — that is what the all-tasks view needs.
type Filter struct {
	Scopes      []task.Scope
	IncludeDone bool
	// DoneSince bounds *done* rows to those completed at or after it. Pending
	// rows are never affected — their done_at is NULL, so a naive
	// `done_at >= ?` would drop every one of them. The zero value applies no
	// bound, which keeps the field additive for callers that predate it.
	DoneSince time.Time
}

// Group is one scope's tasks, for the all-tasks view.
type Group struct {
	Scope task.Scope
	Tasks []task.Task
}

// Add inserts a pending task and returns it with its assigned id and timestamp.
func (db *DB) Add(ctx context.Context, text string, scope task.Scope) (task.Task, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return task.Task{}, errors.New("store: task text is empty")
	}
	scope, err := validScope(scope)
	if err != nil {
		return task.Task{}, err
	}

	created := db.now().Unix()
	res, err := db.ExecContext(ctx,
		`INSERT INTO tasks (text, done, done_at, scope_kind, scope_key, created_at)
		 VALUES (?, 0, NULL, ?, ?, ?)`,
		text, string(scope.Kind), scope.Key, created)
	if err != nil {
		return task.Task{}, fmt.Errorf("add task: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return task.Task{}, fmt.Errorf("add task: %w", err)
	}
	return task.Task{
		ID:        id,
		Text:      text,
		Scope:     scope,
		CreatedAt: time.Unix(created, 0),
	}, nil
}

// Get returns one task by id, or ErrNotFound.
func (db *DB) Get(ctx context.Context, id int64) (task.Task, error) {
	row := db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return task.Task{}, fmt.Errorf("get task %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return task.Task{}, fmt.Errorf("get task %d: %w", id, err)
	}
	return t, nil
}

// List returns matching tasks in merged-popup order: scope tier, newest first.
func (db *DB) List(ctx context.Context, f Filter) ([]task.Task, error) {
	return db.query(ctx, f, listOrder)
}

// ListGrouped returns matching tasks grouped by scope, groups in tier order.
// Scopes with no matching tasks are absent rather than empty.
func (db *DB) ListGrouped(ctx context.Context, f Filter) ([]Group, error) {
	tasks, err := db.query(ctx, f, groupedOrder)
	if err != nil {
		return nil, err
	}
	var groups []Group
	for _, t := range tasks {
		if n := len(groups); n > 0 && groups[n-1].Scope == t.Scope {
			groups[n-1].Tasks = append(groups[n-1].Tasks, t)
			continue
		}
		groups = append(groups, Group{Scope: t.Scope, Tasks: []task.Task{t}})
	}
	return groups, nil
}

// Count returns how many tasks match the filter.
func (db *DB) Count(ctx context.Context, f Filter) (int, error) {
	where, args, err := f.where()
	if err != nil {
		return 0, err
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM tasks`+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count tasks: %w", err)
	}
	return n, nil
}

// Complete marks a task done, stamping done_at.
//
// done_at is what the view's DoneSince bound reads, so it is the moment the row
// starts ageing out of sight — never out of the database. Completing an
// already-done task re-stamps it, which simply restarts that visibility window;
// an earlier version of this comment described it as extending a *purge* window,
// but nothing purges: completion is a toggle (see Uncomplete) and rows are kept
// so history stays possible.
func (db *DB) Complete(ctx context.Context, id int64) error {
	return db.exec(ctx, "complete", id,
		`UPDATE tasks SET done = 1, done_at = ? WHERE id = ?`, db.now().Unix(), id)
}

// Uncomplete returns a task to pending and clears done_at.
func (db *DB) Uncomplete(ctx context.Context, id int64) error {
	return db.exec(ctx, "uncomplete", id,
		`UPDATE tasks SET done = 0, done_at = NULL WHERE id = ?`, id)
}

// UpdateText replaces a task's text.
func (db *DB) UpdateText(ctx context.Context, id int64, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("store: task text is empty")
	}
	return db.exec(ctx, "update text of", id, `UPDATE tasks SET text = ? WHERE id = ?`, text, id)
}

// Rescope moves a task to another scope.
func (db *DB) Rescope(ctx context.Context, id int64, scope task.Scope) error {
	scope, err := validScope(scope)
	if err != nil {
		return err
	}
	return db.exec(ctx, "rescope", id,
		`UPDATE tasks SET scope_kind = ?, scope_key = ? WHERE id = ?`,
		string(scope.Kind), scope.Key, id)
}

// Delete removes a task outright.
func (db *DB) Delete(ctx context.Context, id int64) error {
	return db.exec(ctx, "delete", id, `DELETE FROM tasks WHERE id = ?`, id)
}

// PurgeDone deletes tasks completed strictly before cutoff and reports how many
// went. The cutoff is the caller's: the retention policy belongs to the task
// that owns those semantics, not to the store.
func (db *DB) PurgeDone(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := db.ExecContext(ctx,
		`DELETE FROM tasks WHERE done = 1 AND done_at < ?`, cutoff.Unix())
	if err != nil {
		return 0, fmt.Errorf("purge done tasks: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("purge done tasks: %w", err)
	}
	return int(n), nil
}

func (db *DB) query(ctx context.Context, f Filter, order string) ([]task.Task, error) {
	where, args, err := f.where()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM tasks`+where+` ORDER BY `+order, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var out []task.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("list tasks: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return out, nil
}

// exec runs a single-row statement and turns "no rows matched" into ErrNotFound.
func (db *DB) exec(ctx context.Context, verb string, id int64, query string, args ...any) error {
	res, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("%s task %d: %w", verb, id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s task %d: %w", verb, id, err)
	}
	if n == 0 {
		return fmt.Errorf("%s task %d: %w", verb, id, ErrNotFound)
	}
	return nil
}

// where renders the filter as a WHERE clause plus its arguments.
func (f Filter) where() (string, []any, error) {
	var conds []string
	var args []any

	if !f.IncludeDone {
		conds = append(conds, `done = 0`)
	} else if !f.DoneSince.IsZero() {
		// Only done rows are bounded. Pending rows have a NULL done_at, which
		// no comparison would satisfy, so they need the explicit escape hatch.
		conds = append(conds, `(done = 0 OR done_at >= ?)`)
		args = append(args, f.DoneSince.Unix())
	}
	if len(f.Scopes) > 0 {
		ors := make([]string, 0, len(f.Scopes))
		for _, s := range f.Scopes {
			s, err := validScope(s)
			if err != nil {
				return "", nil, err
			}
			ors = append(ors, `(scope_kind = ? AND scope_key = ?)`)
			args = append(args, string(s.Kind), s.Key)
		}
		conds = append(conds, `(`+strings.Join(ors, ` OR `)+`)`)
	}
	if len(conds) == 0 {
		return "", nil, nil
	}
	return ` WHERE ` + strings.Join(conds, ` AND `), args, nil
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanTask(s scanner) (task.Task, error) {
	var (
		t       task.Task
		done    int
		doneAt  sql.NullInt64
		kind    string
		key     string
		created int64
	)
	if err := s.Scan(&t.ID, &t.Text, &done, &doneAt, &kind, &key, &created); err != nil {
		return task.Task{}, err
	}
	t.Done = done != 0
	if doneAt.Valid {
		at := time.Unix(doneAt.Int64, 0)
		t.DoneAt = &at
	}
	t.Scope = task.Scope{Kind: task.ScopeKind(kind), Key: key}
	t.CreatedAt = time.Unix(created, 0)
	return t, nil
}

// validScope checks a scope and normalizes it: a global task carries no key.
// The same rules are enforced by CHECK constraints in the schema; validating
// here turns a constraint violation into a readable error.
func validScope(s task.Scope) (task.Scope, error) {
	if !s.Kind.Valid() {
		return task.Scope{}, fmt.Errorf("store: unknown scope kind %q", s.Kind)
	}
	if s.Kind == task.ScopeGlobal {
		s.Key = ""
		return s, nil
	}
	if strings.TrimSpace(s.Key) == "" {
		return task.Scope{}, fmt.Errorf("store: %s scope needs a key", s.Kind)
	}
	return s, nil
}
