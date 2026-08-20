-- v2: the tmux session_id -> name map. See docs/design.md "Session renames and
-- stale names" and docs/tasks/2026-08-19-tmux-integration-and-rename-hook.md.
--
-- Why a table at all: a session scope key *is* the session name, and tmux does
-- not tell a session-renamed hook the *old* name — by the time the hook runs,
-- both #{hook_session_name} and #{session_name} are the new one, so the key the
-- tasks are filed under is already gone. The session *id* ($0) does survive a
-- rename, so an id -> name map is the only way to recover the old key.
--
-- It lives here rather than in the XDG state dir so the map is transactionally
-- consistent with the rewrite it drives: a corrupt state file would silently
-- miss a rename, and a missed rename loses a task list rather than a preference.
--
-- The map is best-effort by construction: it only knows sessions in which tdo
-- has run at least once, and tmux ids reset when the server restarts. Stale
-- session scopes stay reachable from the all-tasks view, which owns re-home.

CREATE TABLE sessions (
    session_id TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL,
    updated_at INTEGER NOT NULL,

    -- Absent beats empty, the same rule the scope keys follow: an empty id or
    -- name cannot identify anything, so it is a bug rather than a row.
    CHECK (session_id <> ''),
    CHECK (name <> '')
);
