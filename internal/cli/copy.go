package cli

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// The clipboard copy behind the popup's `y`.
//
// internal/tui decides *what* to copy and hands the text here; this file is the
// only place that turns one into a subprocess or an escape sequence. Same split
// as jump.go, and the invocation table is just as unobvious:
//
//	inside tmux   tmux load-buffer -w -   (text on stdin)
//	outside tmux  ESC ] 52 ; c ; <base64> BEL written to /dev/tty
//
// **Inside tmux the text goes on stdin, never in the argv and never through a
// shell.** A task's text is user data — quotes, `$`, newlines, all legal — and
// `set-buffer <text>` would put it on a command line for no benefit. exec.Command
// with a Stdin reader spawns no shell, so nothing re-parses it. This is the same
// class of bug the rename hook shipped for real (see runSessionRenamed).
//
// `-w` is what forwards the buffer to the *system* clipboard over OSC 52; tmux's
// own paste buffer fills either way, so `prefix ]` works with or without it. Two
// caveats worth knowing and neither fixable here: `-w` needs tmux >= 3.2, which
// is why loadBuffer falls back (below), and a user with `set-clipboard off` gets
// the paste buffer only — tmux is doing what they asked, and no test may depend
// on the option being on.
//
// Outside tmux the escape is written by hand rather than by pulling in a
// clipboard module: the build graph is deliberately at zero new modules (see
// CLAUDE.md on bubbles/textinput), the sequence is fifteen lines, and unlike
// pbcopy/xclip it works over SSH. Its honest weakness is that a terminal which
// ignores OSC 52 is indistinguishable from one that honoured it — so a nil error
// here means "tdo wrote the sequence", not "the clipboard changed". The popup's
// message says `copied:` for that reason rather than anything stronger.

// stdinRunner executes a command with text on its standard input, discarding its
// output. Injected through the package var below so the argv and the stdin can
// both be asserted without a tmux server.
type stdinRunner func(stdin, name string, args ...string) error

// execStdinRunner is the production runner.
func execStdinRunner(stdin, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdin = strings.NewReader(stdin)
	return c.Run()
}

// ttyOpener opens the terminal the OSC 52 escape is written to.
type ttyOpener func() (io.WriteCloser, error)

// openTTY opens the controlling terminal directly rather than using os.Stdout.
// Bubble Tea owns stdout while the popup is up, and /dev/tty is also what
// reaches the terminal when stdout is redirected.
func openTTY() (io.WriteCloser, error) {
	return os.OpenFile("/dev/tty", os.O_WRONLY, 0)
}

// The two seams, as package vars for the same reason runTUIProgram is one: the
// wiring test has to prove the Copy in tui.Config really loads a tmux buffer,
// and it can only reach it through the Config it captured.
var (
	copyRunner stdinRunner = execStdinRunner
	copyTTY    ttyOpener   = openTTY
)

// newCopier builds the tui.Config.Copy for this context.
//
// The branch is decided once, here, rather than on every keypress: whether we
// are inside tmux cannot change while the popup is open.
func newCopier(insideTmux bool) func(string) error {
	if insideTmux {
		return loadBuffer
	}
	return writeOSC52
}

// loadBuffer puts text in tmux's buffer, and thereby on the system clipboard.
//
// The retry is not defensive: `load-buffer -w` is tmux >= 3.2 and this repo
// already has a CI runner on an older tmux than the developer's. Dropping `-w`
// loses the system-clipboard hop and keeps the paste buffer, which is the
// better of the two failures — and it is reported as success, because from the
// popup's side the copy did happen. The alternative would be to overload the
// error return with a warning, which would make DoD 6's "a failing copy says so"
// mean two different things.
func loadBuffer(text string) error {
	if err := copyRunner(text, "tmux", "load-buffer", "-w", "-"); err == nil {
		return nil
	}
	if err := copyRunner(text, "tmux", "load-buffer", "-"); err != nil {
		return fmt.Errorf("tmux load-buffer: %w", err)
	}
	return nil
}

// writeOSC52 asks the terminal to set the system clipboard.
//
// One Write call, deliberately: Bubble Tea is rendering into the same terminal
// from its own goroutine, and a sequence split across two writes could be torn
// by a frame landing between them. A torn escape is not a failed copy — it is
// arbitrary bytes on the user's screen.
func writeOSC52(text string) error {
	w, err := copyTTY()
	if err != nil {
		return fmt.Errorf("open terminal to copy: %w", err)
	}
	defer w.Close()
	if _, err := io.WriteString(w, osc52(text)); err != nil {
		return fmt.Errorf("write clipboard escape: %w", err)
	}
	return nil
}

// osc52 renders the clipboard escape: ESC ] 52 ; c ; <base64> BEL.
//
// `c` is the clipboard selection (as against `p`, the primary selection). The
// payload is base64 precisely so the text needs no escaping — which is what
// makes a task holding a `;`, a newline or an ESC safe to pass through.
func osc52(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
}
