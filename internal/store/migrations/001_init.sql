-- v1 schema. See docs/design.md "Storage".
--
-- Timestamps are INTEGER unix seconds: sortable, timezone-free and a trivial
-- time.Time round-trip. Within one second the id tiebreak keeps newest-first
-- ordering stable.
--
-- The column set is deliberately minimal. Priority, tags, due dates, notes and
-- subtasks are out of v1; adding them later is an ALTER, which is the whole
-- point of having a migration runner from day one.

CREATE TABLE tasks (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    text       TEXT    NOT NULL,
    done       INTEGER NOT NULL DEFAULT 0 CHECK (done IN (0, 1)),
    done_at    INTEGER,
    scope_kind TEXT    NOT NULL CHECK (scope_kind IN ('session', 'dir', 'global')),
    scope_key  TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,

    -- A global task has no key; session and dir tasks must have one.
    CHECK ((scope_kind = 'global' AND scope_key = '') OR
           (scope_kind <> 'global' AND scope_key <> '')),
    -- done and done_at agree: either both set or neither.
    CHECK ((done = 1 AND done_at IS NOT NULL) OR (done = 0 AND done_at IS NULL))
);

-- The popup's merged list filters by scope and pending-ness.
CREATE INDEX idx_tasks_scope ON tasks (scope_kind, scope_key, done);

-- The all-tasks view and every list are newest-first.
CREATE INDEX idx_tasks_created_at ON tasks (created_at DESC, id DESC);
