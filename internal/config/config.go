// Package config reads the user's configuration file: the handful of settings
// that are taste rather than correctness.
//
// It is deliberately separate from internal/scope's sticky preferences, which
// look similar and are not. Those are written by the popup, live in the XDG
// *state* dir, and record what the user last did; these are written by the user,
// live in the XDG *config* dir, and record what the user wants. Different
// authors and different lifecycles, so a corrupt file means different things:
// a bad sticky file is noise to shrug off, a bad config file is a typo somebody
// wants to hear about — which is why Parse reports Problems instead of
// swallowing them.
//
// This package does no I/O beyond reading one file and asks nothing about the
// environment except $XDG_CONFIG_HOME. Parse is a pure function over bytes.
package config

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AppDir is the directory name used under the XDG config dir. It matches
// scope.AppDir by intent and by value, but is redeclared rather than imported:
// this package must not depend on scope, and the two dirs are different places.
const AppDir = "tmux-todo"

// FileName is the config file's name inside AppDir. No extension: the format is
// not one anything else would recognise.
const FileName = "config"

// Placement decides which completed rows are grouped at the end of their scope
// tier, as opposed to staying where they were.
type Placement string

const (
	// Always groups every visible done row below its tier's pending rows.
	Always Placement = "always"
	// Never leaves every done row exactly where it was.
	Never Placement = "never"
	// OnStart groups the rows that were already done when the popup opened, and
	// leaves the ones completed during this session in place. The list is then
	// stable while you are looking at it and tidy every time you arrive.
	OnStart Placement = "on-start"
)

// valid reports whether p is one of the three known placements.
func (p Placement) valid() bool {
	switch p {
	case Always, Never, OnStart:
		return true
	}
	return false
}

// Prefs is the whole configuration.
//
// Named Prefs rather than Config so that tui.Config — which is a different thing,
// the popup's injected dependencies — can hold one without the two names
// colliding at every call site.
type Prefs struct {
	// FollowOnComplete moves the cursor with the row when it is completed.
	// False keeps the cursor at its screen position, so the row that slides up
	// into the vacated slot is selected next.
	//
	// Note this is unobservable under CompleteToBottom OnStart or Never: a row
	// that does not move cannot be followed.
	FollowOnComplete bool
	// FollowOnUncomplete moves the cursor with the row when it returns to
	// pending. True by default and False by default for its sibling, because the
	// two actions mean opposite things: completing is "done with this, show me
	// the next one", uncompleting is "I want this back".
	FollowOnUncomplete bool
	// CompleteToBottom is where done rows sit. See Placement.
	CompleteToBottom Placement
}

// Defaults is the configuration of a machine with no config file. Every fallback
// in this package resolves to exactly this, field by field, so a file that sets
// one key badly behaves identically to a file that omits it.
func Defaults() Prefs {
	return Prefs{
		FollowOnComplete:   false,
		FollowOnUncomplete: true,
		CompleteToBottom:   OnStart,
	}
}

// Problem is one line the parser could not use, and why.
//
// Problems are advisory: every one of them has already been resolved by falling
// back to a default, so nothing needs to act on them. They exist so `tdo doctor`
// can say "line 3: unknown setting" instead of the user re-reading their config
// wondering why it does nothing. Silently ignoring a typo is the failure mode
// this repo keeps shipping.
type Problem struct {
	// Line is 1-indexed, as an editor counts.
	Line int
	// Text is the offending line, trimmed.
	Text string
	// Reason is a lowercase phrase: "unknown setting", "want true or false".
	Reason string
}

func (p Problem) String() string {
	return fmt.Sprintf("line %d: %s: %s", p.Line, p.Reason, p.Text)
}

// Parse reads a config body into Prefs.
//
// It never fails. Anything it cannot use leaves that setting at its default and
// becomes a Problem — a config file must not be a reason the popup does not
// open, and the popup is the only thing that reads one.
//
// The accepted line is `key value`, with an optional `=` between them, `#`
// starting a comment, and blank lines ignored. A repeated key takes its last
// value, which is what every config format does and what someone editing by
// appending expects.
func Parse(body []byte) (Prefs, []Problem) {
	prefs := Defaults()
	var problems []Problem

	sc := bufio.NewScanner(bytes.NewReader(body))
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(stripComment(sc.Text()))
		if text == "" {
			continue
		}
		key, value, ok := split(text)
		if !ok {
			problems = append(problems, Problem{Line: line, Text: text, Reason: "want `setting value`"})
			continue
		}
		if reason := prefs.set(key, value); reason != "" {
			problems = append(problems, Problem{Line: line, Text: text, Reason: reason})
		}
	}
	// A scan error on an in-memory reader means a line longer than bufio's
	// buffer, which is not a config file anyone wrote. Report it against the
	// file rather than pretending the tail was empty.
	if err := sc.Err(); err != nil {
		problems = append(problems, Problem{Reason: "unreadable", Text: err.Error()})
	}
	return prefs, problems
}

// set applies one key/value, returning "" or the reason it could not.
//
// Every unknown key and every bad value leaves the field untouched — the field
// still holds its Defaults() value, because prefs started there — so there is no
// path where a partial parse produces a setting nobody chose.
func (p *Prefs) set(key, value string) string {
	switch key {
	case "follow-on-complete":
		return setBool(&p.FollowOnComplete, value)
	case "follow-on-uncomplete":
		return setBool(&p.FollowOnUncomplete, value)
	case "complete-to-bottom":
		if placement := Placement(value); placement.valid() {
			p.CompleteToBottom = placement
			return ""
		}
		return fmt.Sprintf("want %s, %s or %s", Always, Never, OnStart)
	default:
		return "unknown setting"
	}
}

// setBool is strict on purpose: `yes`, `on` and `1` are not accepted, so there is
// one spelling to remember and a typo is reported rather than guessed at.
func setBool(field *bool, value string) string {
	switch value {
	case "true":
		*field = true
	case "false":
		*field = false
	default:
		return "want true or false"
	}
	return ""
}

// stripComment drops a `#` and everything after it.
//
// Unconditionally, with no quoting rules: no setting this package has takes a
// value that could contain a `#`, and inventing an escape for a format with three
// keys would be more code than the format.
func stripComment(line string) string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		return line[:i]
	}
	return line
}

// split cuts `key value` or `key = value` into its two halves.
func split(line string) (key, value string, ok bool) {
	i := strings.IndexFunc(line, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '='
	})
	if i < 0 {
		return "", "", false
	}
	key = line[:i]
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line[i:]), "="))
	if key == "" || value == "" {
		return "", "", false
	}
	return key, value, true
}

// Load reads the config file at path.
//
// A missing or unreadable file is not a problem and reports none: having no
// config file is the normal case, and a permissions error on one is still a
// machine whose popup should open with the defaults.
func Load(path string) (Prefs, []Problem) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Defaults(), nil
	}
	return Parse(body)
}

// DefaultPath is $XDG_CONFIG_HOME/tmux-todo/config, falling back to
// ~/.config/tmux-todo/config.
//
// The config dir rather than the state dir, which is where the sticky
// preferences live: this file is written by the user and belongs with the rest
// of their dotfiles, where a backup or a dotfiles repo will pick it up.
func DefaultPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, AppDir, FileName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", AppDir, FileName), nil
}
