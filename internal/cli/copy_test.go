package cli

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

// copyCall is one recorded invocation of the stdin runner: the whole argv plus
// what went in on stdin. Both halves are recorded because the bug worth
// catching is the text moving from one to the other.
type copyCall struct {
	stdin string
	name  string
	args  []string
}

// fakeCopyRunner substitutes the stdin runner for one test and returns the
// recorded calls. errs are returned in order, one per call, "" for success —
// which is how the `-w` fallback leg makes only the first call fail.
func fakeCopyRunner(t *testing.T, errs ...string) *[]copyCall {
	t.Helper()
	calls := &[]copyCall{}
	prev := copyRunner
	t.Cleanup(func() { copyRunner = prev })
	copyRunner = func(stdin, name string, args ...string) error {
		n := len(*calls)
		*calls = append(*calls, copyCall{stdin: stdin, name: name, args: args})
		if n < len(errs) && errs[n] != "" {
			return errors.New(errs[n])
		}
		return nil
	}
	return calls
}

// fakeTTY substitutes the terminal the OSC 52 escape is written to, and returns
// the buffer it was written into. This is what makes DoD 3 assertable with no
// terminal at all — a test process inside `go test` has no usable /dev/tty.
func fakeTTY(t *testing.T, openErr error) *strings.Builder {
	t.Helper()
	var buf strings.Builder
	prev := copyTTY
	t.Cleanup(func() { copyTTY = prev })
	copyTTY = func() (io.WriteCloser, error) {
		if openErr != nil {
			return nil, openErr
		}
		return nopWriteCloser{&buf}, nil
	}
	return &buf
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// awkwardTexts are the payloads that would break a shell-string implementation,
// a naive escape, or an argv-based one. Every copy test runs the whole set: the
// difference between these and "hello" is the entire point of the stdin choice.
var awkwardTexts = []string{
	`it's a "$HOME" task`,
	"rebase onto main",
	"semi;colon and ]bracket[",
	"two\nlines",
	"a\ttab",
	`$(rm -rf /) && echo pwned`,
	"unicode ✓ émoji 🎉",
	"",
}

// TestLoadBufferPutsTheTextOnStdin is DoD 2. The argv is pinned exactly, and the
// text has to be in the stdin field and *nowhere* in the argv.
func TestLoadBufferPutsTheTextOnStdin(t *testing.T) {
	for _, text := range awkwardTexts {
		t.Run(fmt.Sprintf("%q", text), func(t *testing.T) {
			calls := fakeCopyRunner(t)

			if err := loadBuffer(text); err != nil {
				t.Fatalf("loadBuffer(%q): %v", text, err)
			}
			if len(*calls) != 1 {
				t.Fatalf("%d calls, want 1: %+v", len(*calls), *calls)
			}
			got := (*calls)[0]

			if got.name != "tmux" {
				t.Errorf("ran %q, want tmux", got.name)
			}
			want := []string{"load-buffer", "-w", "-"}
			if !reflect.DeepEqual(got.args, want) {
				t.Errorf("argv = tmux %v, want tmux %v", got.args, want)
			}
			if got.stdin != text {
				t.Errorf("stdin = %q, want the text verbatim %q", got.stdin, text)
			}
			// The load-bearing negative: not `set-buffer <text>`, and the text
			// nowhere on the command line. A task text is user data and there is
			// no tmux format that escapes it for a shell.
			for _, a := range got.args {
				if a == "set-buffer" {
					t.Error("used set-buffer; the text would be on the command line")
				}
				if text != "" && strings.Contains(a, text) {
					t.Errorf("the text appears in the argv element %q — it must go on stdin only", a)
				}
			}
		})
	}
}

// TestLoadBufferFallsBackWithoutW covers the tmux < 3.2 case: `-w` is what
// forwards to the system clipboard and it did not always exist, so a failure of
// the first form retries without it. The paste buffer still fills, which is why
// this reports success.
func TestLoadBufferFallsBackWithoutW(t *testing.T) {
	calls := fakeCopyRunner(t, "unknown flag: -w")

	if err := loadBuffer("rebase onto main"); err != nil {
		t.Fatalf("loadBuffer: %v, want the fallback to succeed", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("%d calls, want 2 (the -w form then the fallback): %+v", len(*calls), *calls)
	}
	if want := []string{"load-buffer", "-w", "-"}; !reflect.DeepEqual((*calls)[0].args, want) {
		t.Errorf("first call argv = %v, want %v — the -w form must be tried first", (*calls)[0].args, want)
	}
	if want := []string{"load-buffer", "-"}; !reflect.DeepEqual((*calls)[1].args, want) {
		t.Errorf("fallback argv = %v, want %v", (*calls)[1].args, want)
	}
	// Still on stdin, both times. A fallback that moved the text into the argv
	// would be the injection bug reappearing on the rarer path — which is
	// exactly where it would go unnoticed.
	for i, c := range *calls {
		if c.stdin != "rebase onto main" {
			t.Errorf("call %d stdin = %q, want the text", i, c.stdin)
		}
	}
}

// TestLoadBufferReportsAFailedCopy — when both forms fail there is nothing on
// any clipboard, and DoD 6 requires that to reach the popup as an error rather
// than as a cheerful confirmation.
func TestLoadBufferReportsAFailedCopy(t *testing.T) {
	calls := fakeCopyRunner(t, "no server running", "no server running")

	err := loadBuffer("rebase onto main")
	if err == nil {
		t.Fatal("loadBuffer reported success with both forms failing")
	}
	if !strings.Contains(err.Error(), "load-buffer") {
		t.Errorf("error = %q, want it to name the operation", err)
	}
	if !strings.Contains(err.Error(), "no server running") {
		t.Errorf("error = %q, want the underlying cause wrapped", err)
	}
	if len(*calls) != 2 {
		t.Errorf("%d calls, want 2", len(*calls))
	}
}

// TestOSC52IsWellFormed is DoD 3: the sequence is exactly
// ESC ] 52 ; c ; <base64> BEL and the payload decodes back to the text.
//
// Decoded rather than compared against a hand-written base64 string, because the
// assertion that matters is "a terminal would recover the text", and that is
// what decoding checks.
func TestOSC52IsWellFormed(t *testing.T) {
	for _, text := range awkwardTexts {
		t.Run(fmt.Sprintf("%q", text), func(t *testing.T) {
			buf := fakeTTY(t, nil)

			if err := writeOSC52(text); err != nil {
				t.Fatalf("writeOSC52(%q): %v", text, err)
			}
			got := buf.String()

			const prefix = "\x1b]52;c;"
			const terminator = "\a"
			if !strings.HasPrefix(got, prefix) {
				t.Fatalf("sequence = %q, want the ESC ] 52 ; c ; prefix", got)
			}
			if !strings.HasSuffix(got, terminator) {
				t.Fatalf("sequence = %q, want a BEL terminator", got)
			}
			payload := strings.TrimSuffix(strings.TrimPrefix(got, prefix), terminator)
			decoded, err := base64.StdEncoding.DecodeString(payload)
			if err != nil {
				t.Fatalf("payload %q is not base64: %v", payload, err)
			}
			if string(decoded) != text {
				t.Errorf("payload decodes to %q, want %q", decoded, text)
			}
			// The raw text must not be in the sequence next to the base64 —
			// that would mean a terminal renders it as garbage on screen.
			if text != "" && strings.Contains(payload, text) {
				t.Errorf("the sequence carries the text unencoded: %q", got)
			}
		})
	}
}

// TestOSC52IsOneWrite pins the plan's reason for building the string first:
// Bubble Tea renders into the same terminal from its own goroutine, and a
// sequence torn across two writes is arbitrary bytes on the user's screen rather
// than a failed copy.
func TestOSC52IsOneWrite(t *testing.T) {
	var w countingWriter
	prev := copyTTY
	t.Cleanup(func() { copyTTY = prev })
	copyTTY = func() (io.WriteCloser, error) { return nopWriteCloser{&w}, nil }

	if err := writeOSC52("rebase onto main"); err != nil {
		t.Fatalf("writeOSC52: %v", err)
	}
	if w.writes != 1 {
		t.Errorf("the escape took %d writes, want 1 — a frame can land between two", w.writes)
	}
}

type countingWriter struct{ writes int }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.writes++
	return len(p), nil
}

// TestOSC52ReportsAnUnopenableTerminal — `tdo tui` with no controlling terminal
// cannot copy, and must say so rather than silently doing nothing.
func TestOSC52ReportsAnUnopenableTerminal(t *testing.T) {
	fakeTTY(t, errors.New("no such device"))

	err := writeOSC52("rebase onto main")
	if err == nil {
		t.Fatal("writeOSC52 reported success with no terminal to write to")
	}
	if !strings.Contains(err.Error(), "no such device") {
		t.Errorf("error = %q, want the underlying cause", err)
	}
}

// TestNewCopierPicksThePathFromTheEnvironment is the branch itself. Asserted
// behaviourally — by which seam the returned func reaches for — rather than by
// comparing function pointers, which reflect makes awkward and which would not
// notice the two branches being swapped.
func TestNewCopierPicksThePathFromTheEnvironment(t *testing.T) {
	t.Run("inside tmux loads a buffer", func(t *testing.T) {
		calls := fakeCopyRunner(t)
		buf := fakeTTY(t, nil)

		if err := newCopier(true)("rebase onto main"); err != nil {
			t.Fatalf("copy: %v", err)
		}
		if len(*calls) != 1 {
			t.Errorf("%d tmux calls, want 1: %+v", len(*calls), *calls)
		}
		if buf.Len() != 0 {
			t.Errorf("wrote %q to the terminal as well; inside tmux the buffer is the path", buf.String())
		}
	})

	t.Run("outside tmux writes the escape", func(t *testing.T) {
		calls := fakeCopyRunner(t)
		buf := fakeTTY(t, nil)

		if err := newCopier(false)("rebase onto main"); err != nil {
			t.Fatalf("copy: %v", err)
		}
		if buf.Len() == 0 {
			t.Error("nothing written to the terminal outside tmux")
		}
		if len(*calls) != 0 {
			// The failure that matters: `tmux load-buffer` from a plain shell
			// with no server running would just error, so the key would be dead
			// rather than doing the OSC 52 thing.
			t.Errorf("ran tmux %d times outside tmux: %+v", len(*calls), *calls)
		}
	})
}
