package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersionFlagAndCommand(t *testing.T) {
	for _, arg := range []string{"--version", "version"} {
		code, stdout, stderr := run(t, arg)
		if code != 0 {
			t.Errorf("%s: exit code %d, want 0 (stderr: %s)", arg, code, stderr)
		}
		if strings.TrimSpace(stdout) != Version {
			t.Errorf("%s: stdout = %q, want %q", arg, strings.TrimSpace(stdout), Version)
		}
	}
}

func TestNoArgsPrintsUsage(t *testing.T) {
	code, stdout, _ := run(t)
	if code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
	if !strings.Contains(stdout, "usage:") {
		t.Errorf("stdout does not contain usage:\n%s", stdout)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, stderr := run(t, "frobnicate")
	if code != 2 {
		t.Errorf("exit code %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Errorf("stderr does not explain the failure:\n%s", stderr)
	}
}

func TestDoctorOpensDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	code, stdout, stderr := run(t, "doctor", "--db", path)
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	for _, want := range []string{path, "schema   0", "ok"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("doctor output missing %q:\n%s", want, stdout)
		}
	}
}

func TestDoctorReportsBadPath(t *testing.T) {
	// A path whose parent is an existing regular file cannot be created.
	file := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(file, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	code, _, stderr := run(t, "doctor", "--db", filepath.Join(file, "tasks.db"))
	if code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	if stderr == "" {
		t.Error("failure produced no stderr")
	}
}
