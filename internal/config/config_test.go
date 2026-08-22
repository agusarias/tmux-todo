package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaults pins the three defaults, which are the whole product decision
// this package encodes. They are quoted in docs/design.md and in the task brief;
// changing one is a design change, not a refactor.
func TestDefaults(t *testing.T) {
	got := Defaults()
	if got.FollowOnComplete {
		t.Error("follow-on-complete defaults to true, want false")
	}
	if !got.FollowOnUncomplete {
		t.Error("follow-on-uncomplete defaults to false, want true")
	}
	if got.CompleteToBottom != OnStart {
		t.Errorf("complete-to-bottom defaults to %q, want %q", got.CompleteToBottom, OnStart)
	}
}

// TestParse covers the format one line at a time. Each case names what it is
// about, so a failure says which property broke rather than which row of a table.
func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		want     Prefs
		problems int
	}{
		{
			name: "empty body is the defaults",
			body: "",
			want: Defaults(),
		},
		{
			name: "every setting at a non-default value",
			body: "follow-on-complete true\nfollow-on-uncomplete false\ncomplete-to-bottom always\n",
			want: Prefs{FollowOnComplete: true, FollowOnUncomplete: false, CompleteToBottom: Always},
		},
		{
			name: "equals sign is accepted",
			body: "follow-on-complete = true\ncomplete-to-bottom=never\n",
			want: Prefs{FollowOnComplete: true, FollowOnUncomplete: true, CompleteToBottom: Never},
		},
		{
			name: "tabs and extra spaces",
			body: "  follow-on-complete\t\ttrue  \n",
			want: Prefs{FollowOnComplete: true, FollowOnUncomplete: true, CompleteToBottom: OnStart},
		},
		{
			name: "comments and blank lines",
			body: "# a comment\n\n   \nfollow-on-complete true # trailing\n# complete-to-bottom always\n",
			want: Prefs{FollowOnComplete: true, FollowOnUncomplete: true, CompleteToBottom: OnStart},
		},
		{
			name:     "unknown setting falls back and reports",
			body:     "follow-on-complet true\n",
			want:     Defaults(),
			problems: 1,
		},
		{
			name:     "bad boolean falls back and reports",
			body:     "follow-on-uncomplete yes\n",
			want:     Defaults(),
			problems: 1,
		},
		{
			name:     "bad placement falls back and reports",
			body:     "complete-to-bottom sometimes\n",
			want:     Defaults(),
			problems: 1,
		},
		{
			name:     "a bare key with no value is a malformed line",
			body:     "follow-on-complete\n",
			want:     Defaults(),
			problems: 1,
		},
		{
			name:     "a key with an equals and no value is a malformed line",
			body:     "follow-on-complete =\n",
			want:     Defaults(),
			problems: 1,
		},
		{
			name: "last value of a repeated key wins",
			body: "complete-to-bottom always\ncomplete-to-bottom never\n",
			want: Prefs{FollowOnComplete: false, FollowOnUncomplete: true, CompleteToBottom: Never},
		},
		{
			name:     "a bad line does not stop the good ones",
			body:     "nonsense\ncomplete-to-bottom always\n",
			want:     Prefs{FollowOnComplete: false, FollowOnUncomplete: true, CompleteToBottom: Always},
			problems: 1,
		},
		{
			name: "no trailing newline",
			body: "complete-to-bottom never",
			want: Prefs{FollowOnComplete: false, FollowOnUncomplete: true, CompleteToBottom: Never},
		},
		{
			name: "CRLF line endings",
			body: "follow-on-complete true\r\ncomplete-to-bottom always\r\n",
			want: Prefs{FollowOnComplete: true, FollowOnUncomplete: true, CompleteToBottom: Always},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, problems := Parse([]byte(tt.body))
			if got != tt.want {
				t.Errorf("Parse() = %+v, want %+v", got, tt.want)
			}
			if len(problems) != tt.problems {
				t.Errorf("got %d problems %v, want %d", len(problems), problems, tt.problems)
			}
		})
	}
}

// TestProblemNamesTheLine is what makes a problem worth reporting: a count is
// not actionable, a line number and the offending text are.
func TestProblemNamesTheLine(t *testing.T) {
	_, problems := Parse([]byte("# note\n\nfollow-on-complete maybe\n"))
	if len(problems) != 1 {
		t.Fatalf("got %d problems, want 1", len(problems))
	}
	p := problems[0]
	if p.Line != 3 {
		t.Errorf("Line = %d, want 3 (comments and blanks still count)", p.Line)
	}
	if !strings.Contains(p.String(), "follow-on-complete maybe") {
		t.Errorf("String() = %q, want it to quote the offending line", p.String())
	}
	if !strings.Contains(p.String(), "true or false") {
		t.Errorf("String() = %q, want it to say what was expected", p.String())
	}
}

// TestParseNeverFailsOnHostileInput: the popup opens whatever is in this file.
func TestParseNeverFailsOnHostileInput(t *testing.T) {
	bodies := []string{
		"\x00\x01\x02",
		strings.Repeat("=", 10000),
		strings.Repeat("a b\n", 5000),
		"\n\n\n\n",
		"###",
		"= value",
	}
	for _, body := range bodies {
		got, _ := Parse([]byte(body))
		// Whatever it made of it, every field must be a value the rest of the
		// program handles — which for the placement means one of the three.
		if !got.CompleteToBottom.valid() {
			t.Errorf("Parse(%q) produced placement %q, which is not a known value", body, got.CompleteToBottom)
		}
	}
}

// TestLoadMissingFileIsNotAProblem — having no config file is the normal case,
// not a fault, so it must not produce something for doctor to complain about.
func TestLoadMissingFileIsNotAProblem(t *testing.T) {
	got, problems := Load(filepath.Join(t.TempDir(), "nope", "config"))
	if got != Defaults() {
		t.Errorf("Load(missing) = %+v, want the defaults", got)
	}
	if len(problems) != 0 {
		t.Errorf("Load(missing) reported %v, want nothing", problems)
	}
}

// TestLoadUnreadableFileIsNotAProblem — same reasoning: a permissions mistake on
// a config file must not be a reason the popup behaves differently from a
// machine that has none.
func TestLoadUnreadableFileIsNotAProblem(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: an unreadable file is still readable")
	}
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("complete-to-bottom always\n"), 0o200); err != nil {
		t.Fatal(err)
	}
	got, problems := Load(path)
	if got != Defaults() {
		t.Errorf("Load(unreadable) = %+v, want the defaults", got)
	}
	if len(problems) != 0 {
		t.Errorf("Load(unreadable) reported %v, want nothing", problems)
	}
}

// TestLoadReadsTheFile is the positive control for the two tests above: without
// it, a Load that always returned the defaults would pass both of them.
func TestLoadReadsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("complete-to-bottom never\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, problems := Load(path)
	if got.CompleteToBottom != Never {
		t.Errorf("CompleteToBottom = %q, want %q", got.CompleteToBottom, Never)
	}
	if len(problems) != 0 {
		t.Errorf("got problems %v, want none", problems)
	}
}

// TestDefaultPathHonoursXDGConfigHome — and lands in the *config* dir, not the
// state dir the sticky preferences use.
func TestDefaultPathHonoursXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, AppDir, FileName)
	if got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

// TestDefaultPathFallsBackToDotConfig, with XDG_CONFIG_HOME unset — the case
// most machines are actually in.
func TestDefaultPathFallsBackToDotConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}

	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", AppDir, FileName)
	if got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}
