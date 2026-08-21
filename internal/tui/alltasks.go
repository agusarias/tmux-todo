package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

// The all-tasks view.
//
// This is the only surface that can see a task the user has stranded. Every
// other one — the merged list, `tdo list`, the tier filters — is scoped to the
// *current* context by construction, so a task filed under a session that has
// since been renamed or killed is invisible everywhere else. docs/design.md
// makes the rename hook explicitly best-effort *because* this view is the
// backstop.
//
// It reads store.ListGrouped with a nil Filter.Scopes, which is the one place
// in this package that wants "every scope in the database". Everything about
// *right now* — which of those sessions is running — arrives in Config.

// Jump is what internal/cli should do once the popup has closed: switch the
// client to Session. The zero value means "no jump", which is what every exit
// other than Enter produces.
//
// It is a value rather than a subprocess because internal/tui must stay
// environment-blind — it imports no os/exec, asks tmux nothing, and therefore
// cannot be the thing that decides between `tmux switch-client`, `sesh connect`
// and `tmux attach`. That table lives in internal/cli, which already knows
// whether it is inside tmux.
type Jump struct {
	// Session is the tmux session name — a scope key, so it is user data that
	// can contain spaces, quotes and shell metacharacters. internal/cli must
	// pass it as a distinct exec argument and never interpolate it.
	Session string
	// Live is whether that session was running when the popup opened, per
	// Config.LiveSessions. It picks the invocation, not the outcome: a stale
	// false still ends up switching to the session, via create-then-switch.
	Live bool
}

// toggleAllTasks switches between the merged list and the wide one.
//
// Each view keeps its own cursor for the life of the popup: the two lists have
// nothing to do with each other — different rows, different order, headers in
// one and not the other — so carrying one index across would land the cursor on
// an unrelated task.
func (m Model) toggleAllTasks() (tea.Model, tea.Cmd) {
	m.savedCursor[m.view] = m.cursor
	if m.view == viewAll {
		m.view = viewMerged
	} else {
		m.view = viewAll
	}
	m.cursor = m.savedCursor[m.view]
	// The rows for the new view have not arrived yet, so nothing can be drawn
	// from them; the reload's rowsMsg rebuilds and clamps.
	m.vp.SetYOffset(0)
	return m, m.reloadCmd()
}

// isLive reports whether a scope's session is running right now. Only session
// scopes can be anything else — a dir or global group is not a place you can
// switch to, so liveness is not a question there.
func (m Model) isLive(s task.Scope) bool {
	if s.Kind != task.ScopeSession {
		return false
	}
	return m.cfg.LiveSessions[s.Key]
}

// visibleGroups applies to a freshly loaded set of groups everything the view
// hides: the tier filter, and the delete queue.
//
// The two steps are separate on purpose. Filtering the queued ids out of each
// group's Tasks is the obvious half; *then* dropping the groups that came out
// empty is the half that is easy to skip, and skipping it leaves a header with
// nothing under it — a group the user deleted still announcing itself.
func (m Model) visibleGroups(groups []store.Group) []store.Group {
	out := make([]store.Group, 0, len(groups))
	for _, g := range groups {
		if m.filter != "" && g.Scope.Kind != m.filter {
			continue
		}
		// Per group rather than over the concatenation: a group *is* one scope,
		// so partitioning inside it needs no tier detection, and the two views
		// cannot end up disagreeing about where a done row goes.
		tasks := partitionDone(m.dropQueued(g.Tasks))
		if len(tasks) == 0 {
			continue
		}
		out = append(out, store.Group{Scope: g.Scope, Tasks: tasks})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// groupTasks concatenates the groups' tasks in screen order, which is what the
// rest of the model means by m.tasks.
func groupTasks(groups []store.Group) []task.Task {
	var n int
	for _, g := range groups {
		n += len(g.Tasks)
	}
	if n == 0 {
		return nil
	}
	out := make([]task.Task, 0, n)
	for _, g := range groups {
		out = append(out, g.Tasks...)
	}
	return out
}

// jumpToSelected records the jump Enter asked for and starts the exit.
//
// Dir and global rows are a deliberate no-op rather than an error: there is
// nothing to switch to, so the honest response is to do nothing and leave the
// popup where it is.
//
// The exit goes through quit(), which is what makes DoD 12 structural rather
// than remembered: quit commits the queued deletes and only then quits, so the
// jump — which internal/cli runs after Run returns — cannot discard them.
func (m Model) jumpToSelected() (tea.Model, tea.Cmd) {
	if m.view != viewAll {
		return m, nil
	}
	target, ok := m.selectedTask()
	if !ok || target.Scope.Kind != task.ScopeSession || target.Scope.Key == "" {
		return m, nil
	}
	m.jump = Jump{Session: target.Scope.Key, Live: m.isLive(target.Scope)}
	return m.quit()
}

// rehomeGroup moves every task in the group under the cursor to the next
// *currently active* scope.
//
// docs/design.md gives stale groups a re-home but never says re-home to what.
// The active cycle is the answer because re-home exists to make stranded tasks
// visible again, and "visible again" means a scope the user is actually in;
// cycling through every dir key in the database would be a long unordered list
// of places the tasks would still be invisible. It is the same cycle Tab and `s`
// walk, so pressing `r` twice more reaches the third tier — including the
// session scope this client is in, which is the motivating case.
//
// It is bulk and it is not covered by the delete queue's undo: re-homing again
// is the only way back. That is stated in the brief rather than discovered here,
// and it is acceptable because no row is lost.
func (m Model) rehomeGroup() (tea.Model, tea.Cmd) {
	if m.view != viewAll {
		return m, nil
	}
	group, ok := m.cursorGroup()
	if !ok || len(group.Tasks) == 0 {
		return m, nil
	}
	next := m.nextScope(group.Scope.Kind)
	target, ok := m.scopeFor(next)
	if !ok || target == group.Scope {
		// Nowhere else to go — one available tier, or the group is already
		// filed under exactly this scope.
		return m, nil
	}
	ids := taskIDs(group.Tasks)
	// Anchored to the first task so the cursor follows the group into whichever
	// tier it landed in, rather than staying at an index that now belongs to
	// somebody else's group.
	return m, m.writeThenReload(ids[0], func(ctx context.Context, db *store.DB) error {
		for _, id := range ids {
			if err := db.Rescope(ctx, id, target); err != nil {
				return err
			}
		}
		return nil
	})
}

// queueGroupDelete pushes every task in the cursor's group onto the delete
// queue.
//
// It goes through the same queue as `d` rather than issuing a bulk DELETE, which
// is what makes one `u` restore the whole group with every id, timestamp and
// position intact — nothing was written. It also means the product has exactly
// one destructive path instead of a queued one for rows and an immediate one for
// groups.
func (m Model) queueGroupDelete() (tea.Model, tea.Cmd) {
	if m.view != viewAll {
		return m, nil
	}
	group, ok := m.cursorGroup()
	if !ok || len(group.Tasks) == 0 {
		return m, nil
	}
	m.queued = append(m.queued, taskIDs(group.Tasks))
	return m, m.reloadCmd()
}

// startAddInGroup opens the input row under the cursor's group header, filing
// into that group's scope.
//
// The group *is* the scope choice, which is why Tab is inert here: offering a
// second one would be two answers to the same question. The scope may be one
// that is not currently active — a session that is not running — and that is
// legitimate rather than a bug to guard against: queueing work for a session you
// are about to recreate is arguably the point of this view. It is also why the
// scope is carried whole instead of being looked up by kind, since scopeFor only
// knows the active set.
func (m Model) startAddInGroup() (tea.Model, tea.Cmd) {
	group, ok := m.cursorGroup()
	if !ok {
		return m, nil
	}
	m.mode = modeInput
	m.inputKind = inputAdd
	m.inputTarget = 0
	m.inputScope = group.Scope.Kind
	m.inputPlace = group.Scope
	m.inputHint = ""
	m.input.reset()
	m.refreshViewport()
	return m, nil
}

// taskIDs pulls the ids out of a group's tasks, so the write closure captures a
// plain slice rather than the model's rows.
func taskIDs(tasks []task.Task) []int64 {
	out := make([]int64, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}
