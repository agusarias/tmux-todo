package scope

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// norm is what every expectation has to go through: t.TempDir() on macOS lives
// under a symlink (/var -> /private/var), so a raw temp path is never the key.
func norm(t *testing.T, path string) string {
	t.Helper()
	got, err := normalizePath(path)
	if err != nil {
		t.Fatalf("normalizePath(%s): %v", path, err)
	}
	return got
}

func mkdirAll(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	return path
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestDirKeyFromGitDirectory(t *testing.T) {
	root := t.TempDir()
	repo := mkdirAll(t, filepath.Join(root, "repo"))
	mkdirAll(t, filepath.Join(repo, ".git"))
	deep := mkdirAll(t, filepath.Join(repo, "a", "b", "c"))

	got, err := DirKey(deep)
	if err != nil {
		t.Fatalf("DirKey: %v", err)
	}
	if want := norm(t, repo); got != want {
		t.Errorf("DirKey = %q, want %q", got, want)
	}
}

// TestDirKeyFoldsWorktreeIntoMainRepo is the rule that matters most day to day:
// a taskflow worktree shares its parent project's task list.
func TestDirKeyFoldsWorktreeIntoMainRepo(t *testing.T) {
	root := t.TempDir()
	repo := mkdirAll(t, filepath.Join(root, "todo"))
	mkdirAll(t, filepath.Join(repo, ".git", "worktrees", "todo-scope"))

	worktree := mkdirAll(t, filepath.Join(root, "todo-scope"))
	writeFile(t, filepath.Join(worktree, ".git"),
		"gitdir: "+filepath.Join(repo, ".git", "worktrees", "todo-scope")+"\n")
	deep := mkdirAll(t, filepath.Join(worktree, "internal", "scope"))

	for _, start := range []string{worktree, deep} {
		got, err := DirKey(start)
		if err != nil {
			t.Fatalf("DirKey(%s): %v", start, err)
		}
		if want := norm(t, repo); got != want {
			t.Errorf("DirKey(%s) = %q, want the main repo %q", start, got, want)
		}
	}
}

// A relative gitdir: pointer is legal and git writes them in some setups.
func TestDirKeyHandlesRelativeGitdir(t *testing.T) {
	root := t.TempDir()
	repo := mkdirAll(t, filepath.Join(root, "todo"))
	mkdirAll(t, filepath.Join(repo, ".git", "worktrees", "wt"))

	worktree := mkdirAll(t, filepath.Join(root, "wt"))
	writeFile(t, filepath.Join(worktree, ".git"), "gitdir: ../todo/.git/worktrees/wt\n")

	got, err := DirKey(worktree)
	if err != nil {
		t.Fatalf("DirKey: %v", err)
	}
	if want := norm(t, repo); got != want {
		t.Errorf("DirKey = %q, want %q", got, want)
	}
}

// A submodule's .git file points into .git/modules/, not .git/worktrees/, so it
// keeps its own root: it is a separate repository and deserves its own list.
func TestDirKeySubmoduleKeepsItsOwnRoot(t *testing.T) {
	root := t.TempDir()
	repo := mkdirAll(t, filepath.Join(root, "parent"))
	mkdirAll(t, filepath.Join(repo, ".git", "modules", "sub"))

	sub := mkdirAll(t, filepath.Join(repo, "sub"))
	writeFile(t, filepath.Join(sub, ".git"),
		"gitdir: "+filepath.Join(repo, ".git", "modules", "sub")+"\n")

	got, err := DirKey(sub)
	if err != nil {
		t.Fatalf("DirKey: %v", err)
	}
	if want := norm(t, sub); got != want {
		t.Errorf("DirKey = %q, want the submodule itself %q", got, want)
	}
}

// A repository that merely lives in a directory called "worktrees" must not be
// mistaken for a linked worktree.
func TestDirKeyIgnoresWorktreesSegmentOutsideGitDir(t *testing.T) {
	root := t.TempDir()
	elsewhere := mkdirAll(t, filepath.Join(root, "elsewhere", "worktrees", "thing"))
	dir := mkdirAll(t, filepath.Join(root, "project"))
	writeFile(t, filepath.Join(dir, ".git"), "gitdir: "+elsewhere+"\n")

	got, err := DirKey(dir)
	if err != nil {
		t.Fatalf("DirKey: %v", err)
	}
	if want := norm(t, dir); got != want {
		t.Errorf("DirKey = %q, want %q", got, want)
	}
}

func TestDirKeyWithoutRepoIsLiteral(t *testing.T) {
	notes := mkdirAll(t, filepath.Join(t.TempDir(), "notes", "deep"))

	got, err := DirKey(notes)
	if err != nil {
		t.Fatalf("DirKey: %v", err)
	}
	if want := norm(t, notes); got != want {
		t.Errorf("DirKey = %q, want the literal path %q", got, want)
	}
}

// TestDirKeyIsSymlinkStable is the one that keeps a user from ending up with two
// task lists for one directory.
func TestDirKeyIsSymlinkStable(t *testing.T) {
	root := t.TempDir()
	repo := mkdirAll(t, filepath.Join(root, "repo"))
	mkdirAll(t, filepath.Join(repo, ".git"))
	deep := mkdirAll(t, filepath.Join(repo, "pkg"))

	link := filepath.Join(root, "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	viaReal, err := DirKey(deep)
	if err != nil {
		t.Fatalf("DirKey(real): %v", err)
	}
	viaLink, err := DirKey(filepath.Join(link, "pkg"))
	if err != nil {
		t.Fatalf("DirKey(link): %v", err)
	}
	if viaReal != viaLink {
		t.Errorf("symlinked paths produced two keys:\n  %q\n  %q", viaReal, viaLink)
	}
}

func TestDirKeyRejectsMissingPath(t *testing.T) {
	if _, err := DirKey(filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Error("DirKey accepted a nonexistent path; a broken path must not become a key")
	}
	if _, err := DirKey(""); err == nil {
		t.Error("DirKey accepted an empty path")
	}
}

func TestDirKeyHasNoTrailingSeparator(t *testing.T) {
	repo := mkdirAll(t, filepath.Join(t.TempDir(), "repo"))
	mkdirAll(t, filepath.Join(repo, ".git"))

	got, err := DirKey(repo + string(filepath.Separator))
	if err != nil {
		t.Fatalf("DirKey: %v", err)
	}
	if strings.HasSuffix(got, string(filepath.Separator)) {
		t.Errorf("key %q has a trailing separator", got)
	}
	if want := norm(t, repo); got != want {
		t.Errorf("DirKey = %q, want %q", got, want)
	}
}

func TestParseGitFileErrors(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"empty":       "",
		"no-gitdir":   "something else\n",
		"empty-value": "gitdir:   \n",
	} {
		path := filepath.Join(dir, name)
		writeFile(t, path, body)
		if _, err := parseGitFile(path); err == nil {
			t.Errorf("%s: parseGitFile returned no error", name)
		}
	}
}

// TestAgreesWithGitBinary is what makes the pure-Go walker trustworthy: it pins
// our answer to the real git's on a repo and on a linked worktree. Skipped only
// when git is genuinely absent.
func TestAgreesWithGitBinary(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed; the walker's own tests use hand-built fixtures")
	}

	root := t.TempDir()
	repo := mkdirAll(t, filepath.Join(root, "repo"))
	// -c keeps the fixture hermetic: no dependency on the machine's git config.
	git := func(dir string, args ...string) string {
		t.Helper()
		full := append([]string{
			"-C", dir,
			"-c", "user.name=taskflow",
			"-c", "user.email=taskflow@example.invalid",
			"-c", "commit.gpgsign=false",
			"-c", "init.defaultBranch=main",
		}, args...)
		out, err := exec.Command(gitBin, full...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	git(repo, "init")
	writeFile(t, filepath.Join(repo, "README"), "fixture\n")
	git(repo, "add", "README")
	git(repo, "commit", "-m", "initial")

	sub := mkdirAll(t, filepath.Join(repo, "pkg", "deep"))
	worktree := filepath.Join(root, "repo-wt")
	git(repo, "worktree", "add", worktree, "-b", "wt")
	wtSub := mkdirAll(t, filepath.Join(worktree, "pkg"))

	// Inside the main repo, git's toplevel is the answer.
	for _, start := range []string{repo, sub} {
		want := norm(t, git(start, "rev-parse", "--show-toplevel"))
		got, err := DirKey(start)
		if err != nil {
			t.Fatalf("DirKey(%s): %v", start, err)
		}
		if got != want {
			t.Errorf("DirKey(%s) = %q, git says %q", start, got, want)
		}
	}

	// Inside a linked worktree, git's --git-common-dir points at the main repo's
	// .git, whose parent is the root we deliberately fold into.
	for _, start := range []string{worktree, wtSub} {
		commonDir := git(start, "rev-parse", "--path-format=absolute", "--git-common-dir")
		want := norm(t, filepath.Dir(commonDir))
		got, err := DirKey(start)
		if err != nil {
			t.Fatalf("DirKey(%s): %v", start, err)
		}
		if got != want {
			t.Errorf("DirKey(%s) = %q, git's main repo is %q", start, got, want)
		}
		// And it is emphatically not the worktree's own toplevel.
		if wtTop := norm(t, git(start, "rev-parse", "--show-toplevel")); got == wtTop {
			t.Errorf("DirKey(%s) returned the worktree toplevel %q instead of the main repo", start, wtTop)
		}
	}

	// A submodule is a separate repository, so our "it keeps its own root" rule
	// should agree with git rather than diverge from it. The plan flagged this as
	// an assumption to confirm against the real thing, so confirm it.
	inner := mkdirAll(t, filepath.Join(root, "inner"))
	git(inner, "init")
	writeFile(t, filepath.Join(inner, "f"), "x\n")
	git(inner, "add", "f")
	git(inner, "commit", "-m", "inner")

	// Local-path submodules need this opt-in on current git versions.
	out, addErr := exec.Command(gitBin, "-C", repo,
		"-c", "user.name=taskflow", "-c", "user.email=taskflow@example.invalid",
		"-c", "protocol.file.allow=always",
		"submodule", "add", inner, "vendor/inner").CombinedOutput()
	if addErr != nil {
		t.Logf("skipping the submodule leg: git submodule add failed: %v\n%s", addErr, out)
		return
	}
	subRepo := filepath.Join(repo, "vendor", "inner")
	want := norm(t, git(subRepo, "rev-parse", "--show-toplevel"))
	got, err := DirKey(subRepo)
	if err != nil {
		t.Fatalf("DirKey(submodule): %v", err)
	}
	if got != want {
		t.Errorf("DirKey(submodule) = %q, git says %q", got, want)
	}
	if got == norm(t, repo) {
		t.Errorf("submodule folded into the parent repo %q; it is a separate repository", got)
	}
}
