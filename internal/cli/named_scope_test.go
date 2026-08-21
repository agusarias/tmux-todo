package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agusarias/tmux-todo/internal/task"
)

// --- --session <name> ------------------------------------------------------

// DoD 1: the name is the key, verbatim, and the *other* sessions stay out. The
// bug this guards against is not "returns nothing" but "returns too much" — a
// selector that fell through to the active set would look right on a database
// with one session in it.
func TestListSessionByNameSelectsExactlyThatSession(t *testing.T) {
	dir := t.TempDir()
	fakeContext{session: "pulsar", sessionID: "$3", dir: dir}.install(t)
	path := newDB(t)
	seed(t, path,
		task.Task{Text: "backend one", Scope: task.Scope{Kind: task.ScopeSession, Key: "backend"}},
		task.Task{Text: "backend two", Scope: task.Scope{Kind: task.ScopeSession, Key: "backend"}},
		task.Task{Text: "here", Scope: task.Scope{Kind: task.ScopeSession, Key: "pulsar"}},
		task.Task{Text: "a dir task", Scope: task.Scope{Kind: task.ScopeDir, Key: dirKey(t, dir)}},
		task.Task{Text: "a global task", Scope: task.Scope{Kind: task.ScopeGlobal}},
	)

	code, stdout, stderr := run(t, "list", "--db", path, "--session", "backend")
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	got := textsOf(stdout)
	want := []string{"backend two", "backend one"} // newest first, as store.List orders
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("rows = %v, want %v\n%s", got, want, stdout)
	}
	for _, leaked := range []string{"here", "a dir task", "a global task"} {
		if strings.Contains(stdout, leaked) {
			t.Errorf("--session backend leaked %q — the selector fell through to another scope", leaked)
		}
	}

	// DoD 1's other half: count agrees. It shares filter() precisely so that it
	// cannot disagree, and this is the assertion that proves the flag was not
	// added to list alone.
	code, stdout, stderr = run(t, "count", "--db", path, "--session", "backend")
	if code != 0 {
		t.Fatalf("count: exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	if strings.TrimSpace(stdout) != "2" {
		t.Errorf("count --session backend = %q, want 2", strings.TrimSpace(stdout))
	}
}

// DoD 2: a named session is a question about stored rows, so it must not consult
// tmux and must not fail. $TMUX unset is the case a script or a statusline
// outside tmux actually hits.
//
// The resolver here fails the test if it is queried at all, which is stronger
// than asserting exit 0: a tmux call that happened to succeed would leave the
// flag quietly dependent on an environment it has no business reading.
func TestNamedSelectorsNeverConsultTmux(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"session with tasks", []string{"--session", "backend"}, "backend one"},
		{"session with none", []string{"--session", "no-such-session"}, ""},
		{"dir with tasks", []string{"--dir", "/somewhere/else"}, "elsewhere"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No session, no dir: outside tmux, and with no working directory
			// either, so the active set is global-only and Lookup would fail for
			// both session and dir. Nothing here may need them.
			fakeContext{}.install(t)
			path := newDB(t)
			seed(t, path,
				task.Task{Text: "backend one", Scope: task.Scope{Kind: task.ScopeSession, Key: "backend"}},
				task.Task{Text: "elsewhere", Scope: task.Scope{Kind: task.ScopeDir, Key: "/somewhere/else"}},
			)

			code, stdout, stderr := run(t, append([]string{"list", "--db", path}, tc.args...)...)
			if code != 0 {
				t.Fatalf("exit code %d, want 0 — a named scope must not error outside tmux (stderr: %s)", code, stderr)
			}
			got := strings.Join(textsOf(stdout), "|")
			if got != tc.want {
				t.Errorf("rows = %q, want %q", got, tc.want)
			}
		})
	}
}

// A session that is not running and a session that never existed are the same
// question, and both are the point of the flag rather than an error.
func TestListSessionByNameWithNoTasksIsAnEmptySuccess(t *testing.T) {
	fakeContext{session: "pulsar", sessionID: "$3", dir: t.TempDir()}.install(t)
	path := newDB(t)
	seed(t, path, task.Task{Text: "here", Scope: task.Scope{Kind: task.ScopeSession, Key: "pulsar"}})

	code, stdout, stderr := run(t, "list", "--db", path, "--session", "killed-yesterday")
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty: an empty list is a success", stderr)
	}
	// ...and as JSON it is the contract's empty array, never null.
	code, stdout, _ = run(t, "list", "--db", path, "--session", "killed-yesterday", "--json")
	if code != 0 || strings.TrimSpace(stdout) != `{"tasks":[]}` {
		t.Errorf("json = %q (code %d), want {\"tasks\":[]}", stdout, code)
	}
}

// The name is used verbatim, which matters because session keys have no
// normalisation anywhere in internal/scope: cleaning, trimming or case-folding
// here would look helpful and silently miss the rows.
func TestListSessionNameIsUsedVerbatim(t *testing.T) {
	for _, name := range []string{
		"Backend",          // case is significant
		"weird:name",       // a name tmux cannot target, but a perfectly good key
		`it's a "session"`, // quotes survive: nothing here goes through a shell
		" leading-space",   // not trimmed
		"trailing-slash/",  // not cleaned the way a path would be
	} {
		t.Run(name, func(t *testing.T) {
			fakeContext{}.install(t)
			path := newDB(t)
			seed(t, path,
				task.Task{Text: "the one", Scope: task.Scope{Kind: task.ScopeSession, Key: name}},
				// A neighbour that a normalising implementation might collapse onto.
				task.Task{Text: "not the one", Scope: task.Scope{Kind: task.ScopeSession, Key: strings.ToLower(strings.TrimSpace(strings.TrimSuffix(name, "/"))) + "-x"}},
			)

			code, stdout, stderr := run(t, "list", "--db", path, "--session", name)
			if code != 0 {
				t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
			}
			if got := strings.Join(textsOf(stdout), "|"); got != "the one" {
				t.Errorf("rows = %q, want %q — the name was not used verbatim", got, "the one")
			}
		})
	}
}

// --- the dash guard (DoD 3 and 6) ------------------------------------------

// The failure being prevented is silent: without this guard `--session --json`
// sets the name to "--json", drops --json, and exits 0 with an empty list. So
// both directions are pinned — the rejection, and the "=" escape that has to
// keep working for a session genuinely called "-json".
func TestDashLeadingSelectorValueIsAUsageError(t *testing.T) {
	path := newDB(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"list --session --json", []string{"list", "--db", path, "--session", "--json"}},
		{"list --dir --json", []string{"list", "--db", path, "--dir", "--json"}},
		{"count --session --pending", []string{"count", "--db", path, "--session", "--pending"}},
		{"count --dir --pending", []string{"count", "--db", path, "--dir", "--pending"}},
		{"list --session --all", []string{"list", "--db", path, "--session", "--all"}},
		// --scope was only ever safe here by accident: "--json" happens not to be
		// a valid scope kind. Now it fails for the actual reason.
		{"list --scope --json", []string{"list", "--db", path, "--scope", "--json"}},
		// The value-taking flag that existed before this task.
		{"list --db --json", []string{"list", "--db", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeContext{}.install(t)
			code, stdout, stderr := run(t, tc.args...)
			if code != 2 {
				t.Errorf("exit code %d, want 2 (stdout %q, stderr %q)", code, stdout, stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty — a rejected command must produce no output", stdout)
			}
			// The message has to name the problem AND the way out, or the guard
			// trades a silent bug for a dead end.
			for _, want := range []string{"needs a value", "="} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr = %q, want it to contain %q", stderr, want)
				}
			}
		})
	}
}

// The escape, and the proof that the following flag survives it: --json is
// honoured, which is exactly what the unguarded version silently ate.
func TestDashLeadingSelectorValueIsPassedWithEquals(t *testing.T) {
	fakeContext{}.install(t)
	path := newDB(t)
	seed(t, path,
		task.Task{Text: "in the odd session", Scope: task.Scope{Kind: task.ScopeSession, Key: "-json"}},
		task.Task{Text: "somewhere normal", Scope: task.Scope{Kind: task.ScopeSession, Key: "backend"}},
	)

	code, stdout, stderr := run(t, "list", "--db", path, "--session=-json", "--json")
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, `"key":"-json"`) {
		t.Errorf("stdout = %q, want the session keyed -json", stdout)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), `{"tasks":[`) {
		t.Errorf("stdout = %q — --json was dropped, which is the silent failure the guard exists for", stdout)
	}
	if strings.Contains(stdout, "somewhere normal") {
		t.Errorf("stdout = %q, want only the -json session", stdout)
	}
}

// --- --dir <path> (DoD 4 and 5) --------------------------------------------

// DoD 4, as a round trip through the production path: file with `add --dir` from
// one spelling, find it with `list --dir` from another. Both go through
// scope.DirKey, so this fails the moment --dir grows its own normalisation.
func TestListDirNormalisesLikeAddDoes(t *testing.T) {
	repo := t.TempDir()
	sub := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// A git repo, so DirKey folds the subdirectory onto the root rather than
	// answering with the subdirectory itself.
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatalf("setup symlink: %v", err)
	}

	fakeContext{dir: sub}.install(t)
	path := newDB(t)
	// Filed from the deep subdirectory, through the same normaliser.
	if code, _, stderr := run(t, "add", "--db", path, "the task", "--dir"); code != 0 {
		t.Fatalf("add: exit code %d, want 0 (stderr: %s)", code, stderr)
	}

	// Three spellings of one directory, all of which must find it.
	for _, spelling := range []string{repo, sub, link, filepath.Join(sub, "..", "..")} {
		t.Run(spelling, func(t *testing.T) {
			code, stdout, stderr := run(t, "list", "--db", path, "--dir", spelling)
			if code != 0 {
				t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
			}
			if got := strings.Join(textsOf(stdout), "|"); got != "the task" {
				t.Errorf("rows = %q, want %q — %q did not normalise to the key add filed under",
					got, "the task", spelling)
			}
		})
	}

	// And count, for the same reason as the session case.
	if code, stdout, _ := run(t, "count", "--db", path, "--dir", sub); code != 0 || strings.TrimSpace(stdout) != "1" {
		t.Errorf("count --dir = %q (code %d), want 1", strings.TrimSpace(stdout), code)
	}
}

// A relative spelling has to work too, since "." is how a user would actually
// type it. The cwd is this test process's, so the assertion is that --dir "."
// and --dir <that same path> agree rather than that either equals a fixed value.
func TestListDirAcceptsARelativeSpelling(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	fakeContext{}.install(t)
	path := newDB(t)
	seed(t, path, task.Task{Text: "the task", Scope: task.Scope{Kind: task.ScopeDir, Key: dirKey(t, wd)}})

	for _, spelling := range []string{".", wd} {
		code, stdout, stderr := run(t, "list", "--db", path, "--dir", spelling)
		if code != 0 {
			t.Fatalf("--dir %q: exit code %d, want 0 (stderr: %s)", spelling, code, stderr)
		}
		if got := strings.Join(textsOf(stdout), "|"); got != "the task" {
			t.Errorf("--dir %q: rows = %q, want %q", spelling, got, "the task")
		}
	}
}

// DoD 5: scope.DirKey lstats the path, so it errors on a directory that is gone —
// and a deleted project's stranded list is exactly what this flag is for. It
// falls back to a cleaned absolute path rather than failing.
//
// The limitation is asserted too, not just documented: the fallback cannot
// symlink-resolve, so a key stored through a symlinked path does NOT match. That
// is a real, known gap; a test that only covered the happy direction would let it
// turn into a silent regression the day someone "fixed" the fallback.
func TestListDirOfADeletedDirectoryStillQueries(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "deleted-project")
	fakeContext{}.install(t)
	path := newDB(t)
	seed(t, path,
		task.Task{Text: "stranded", Scope: task.Scope{Kind: task.ScopeDir, Key: gone}},
		task.Task{Text: "elsewhere", Scope: task.Scope{Kind: task.ScopeDir, Key: "/other/place"}},
	)

	// The path does not exist, and an uncleaned spelling of it must still land.
	for _, spelling := range []string{gone, filepath.Join(gone, "..", "deleted-project")} {
		code, stdout, stderr := run(t, "list", "--db", path, "--dir", spelling)
		if code != 0 {
			t.Fatalf("--dir %q: exit code %d, want 0 — a deleted directory must still be"+
				" queryable (stderr: %s)", spelling, code, stderr)
		}
		if got := strings.Join(textsOf(stdout), "|"); got != "stranded" {
			t.Errorf("--dir %q: rows = %q, want %q", spelling, got, "stranded")
		}
	}
}

// The documented limitation, pinned. On macOS t.TempDir() is already under a
// symlink (/var -> /private/var), so a key stored resolved is not reachable by
// the unresolved spelling once the directory is gone.
func TestListDirOfADeletedSymlinkedPathIsTheDocumentedGap(t *testing.T) {
	real := t.TempDir()
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if resolved == real {
		t.Skip("t.TempDir() is not under a symlink here, so there is no gap to demonstrate")
	}
	gone := filepath.Join(real, "gone")
	fakeContext{}.install(t)
	path := newDB(t)
	// Stored the way DirKey would have stored it while the directory existed.
	seed(t, path, task.Task{Text: "stranded", Scope: task.Scope{Kind: task.ScopeDir, Key: filepath.Join(resolved, "gone")}})

	code, stdout, stderr := run(t, "list", "--db", path, "--dir", gone)
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	if strings.Contains(stdout, "stranded") {
		t.Errorf("--dir %q found the task. That is better than documented — if this is now"+
			" intentional, update the usage text and this test together.", gone)
	}
	// ...and --scope=all is the way out, which is what the usage text promises.
	code, stdout, stderr = run(t, "list", "--db", path, "--scope=all")
	if code != 0 {
		t.Fatalf("--scope=all: exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "stranded") {
		t.Error("--scope=all did not show the stranded key, so the documented escape hatch is wrong")
	}
}

// --- mutual exclusion (DoD 7) ----------------------------------------------

func TestSelectorsAreMutuallyExclusive(t *testing.T) {
	path := newDB(t)
	for _, tc := range []struct {
		name  string
		args  []string
		wants []string
	}{
		{"list scope+session", []string{"list", "--db", path, "--scope=all", "--session", "backend"}, []string{"--scope", "--session"}},
		{"list scope+dir", []string{"list", "--db", path, "--scope=all", "--dir", "/tmp"}, []string{"--scope", "--dir"}},
		{"list session+dir", []string{"list", "--db", path, "--session", "backend", "--dir", "/tmp"}, []string{"--session", "--dir"}},
		{"list all three", []string{"list", "--db", path, "--scope=all", "--session", "b", "--dir", "/tmp"}, []string{"--scope", "--session", "--dir"}},
		{"count scope+session", []string{"count", "--db", path, "--scope=all", "--session", "backend"}, []string{"--scope", "--session"}},
		{"count session+dir", []string{"count", "--db", path, "--session", "b", "--dir", "/tmp"}, []string{"--session", "--dir"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeContext{}.install(t)
			code, stdout, stderr := run(t, tc.args...)
			if code != 2 {
				t.Errorf("exit code %d, want 2 (stderr: %s)", code, stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			// The message must name which flags clashed — "mutually exclusive"
			// alone leaves the user to guess on a long command line.
			for _, want := range tc.wants {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr = %q, want it to name %s", stderr, want)
				}
			}
		})
	}
}

// An empty value is a usage error rather than a silent fall-through to the active
// set. `--session=""` would otherwise list *here* while reading as a question
// about somewhere else — "absent beats empty", the rule every other scope key in
// this repo follows.
func TestEmptySelectorValueIsAUsageError(t *testing.T) {
	path := newDB(t)
	for _, args := range [][]string{
		{"list", "--db", path, "--session="},
		{"list", "--db", path, "--dir="},
		{"count", "--db", path, "--session="},
	} {
		fakeContext{session: "pulsar", sessionID: "$3", dir: t.TempDir()}.install(t)
		code, stdout, stderr := run(t, args...)
		if code != 2 {
			t.Errorf("%v: exit code %d, want 2 (stdout %q, stderr %q)", args, code, stdout, stderr)
		}
		if stdout != "" {
			t.Errorf("%v: stdout = %q, want empty", args, stdout)
		}
	}
}

// --- composition (DoD 8) ---------------------------------------------------

// The selectors compose with every other flag, in either order. parseArgs is what
// makes order irrelevant, and pinning it for the new flags is cheaper than
// assuming the property transferred.
func TestNamedSelectorsComposeInAnyOrder(t *testing.T) {
	fakeContext{}.install(t)
	path := newDB(t)
	seed(t, path,
		task.Task{Text: "pending one", Scope: task.Scope{Kind: task.ScopeSession, Key: "backend"}},
		task.Task{Text: "done one", Scope: task.Scope{Kind: task.ScopeSession, Key: "backend"}},
	)
	// Complete the second task so --all and --pending have something to move.
	if code, _, stderr := run(t, "done", "--db", path, "2"); code != 0 {
		t.Fatalf("done: exit code %d (stderr: %s)", code, stderr)
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"selector first", []string{"list", "--session", "backend", "--db", path, "--all"}, "done one|pending one"},
		{"selector last", []string{"list", "--db", path, "--all", "--session", "backend"}, "done one|pending one"},
		{"selector between", []string{"list", "--db", path, "--session", "backend", "--all"}, "done one|pending one"},
		{"pending default", []string{"list", "--db", path, "--session", "backend"}, "pending one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := run(t, tc.args...)
			if code != 0 {
				t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
			}
			if got := strings.Join(textsOf(stdout), "|"); got != tc.want {
				t.Errorf("rows = %q, want %q", got, tc.want)
			}
		})
	}

	// count's polarity is the opposite of list's, and it is the shared filter()
	// that keeps that from being decided twice. --json is list-only.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"count", "--db", path, "--session", "backend"}, "2"},
		{[]string{"count", "--session", "backend", "--db", path, "--pending"}, "1"},
		{[]string{"count", "--db", path, "--pending", "--session", "backend"}, "1"},
	} {
		code, stdout, stderr := run(t, tc.args...)
		if code != 0 {
			t.Fatalf("%v: exit code %d (stderr: %s)", tc.args, code, stderr)
		}
		if got := strings.TrimSpace(stdout); got != tc.want {
			t.Errorf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

// DoD 9: the usage text documents the flags, the exclusion, the "=" escape and
// the --dir limitation. A flag nobody can discover is only half-shipped.
func TestUsageDocumentsTheNamedSelectors(t *testing.T) {
	_, stdout, _ := run(t, "help")
	for _, want := range []string{
		"--session <name>",
		"--dir <path>",
		"verbatim",
		"mutually exclusive",
		"--session=-json",
		"--scope=all to find stranded dir keys",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("usage does not document %q", want)
		}
	}
}
