package scope

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DirKey normalizes a directory into the key dir-scoped tasks are filed under.
//
// It walks up looking for a `.git` entry. A `.git` directory means that
// directory is the repo root. A `.git` file is a linked worktree or a submodule:
// its `gitdir:` line is followed, and when it points inside a `worktrees/`
// segment the main repo root is used instead, so a taskflow worktree shares its
// parent project's task list. A path with no `.git` above it is used literally.
//
// The result is absolute, cleaned, symlink-resolved and never case-folded —
// these strings are database keys, and two spellings of one directory must not
// become two lists.
func DirKey(path string) (string, error) {
	start, err := normalizePath(path)
	if err != nil {
		return "", err
	}
	root, err := RepoRoot(start)
	if err != nil {
		return "", err
	}
	// The root may have arrived via a gitdir: pointer, which can itself contain
	// symlinks, so normalize once more rather than trusting the walk.
	return normalizePath(root)
}

// RepoRoot returns the git repo root governing dir, or dir itself when there is
// no repository above it. dir is expected to be absolute and already normalized.
func RepoRoot(dir string) (string, error) {
	for current := dir; ; {
		dotGit := filepath.Join(current, ".git")
		info, err := os.Lstat(dotGit)
		switch {
		case err == nil && info.IsDir():
			return current, nil
		case err == nil && info.Mode().IsRegular():
			return rootFromGitFile(current, dotGit)
		}

		parent := filepath.Dir(current)
		if parent == current {
			// Reached the filesystem root without finding a repository: the
			// original path is its own scope (`~/notes` is a legitimate key).
			return dir, nil
		}
		current = parent
	}
}

// rootFromGitFile handles a `.git` file: a linked worktree folds into its main
// repo, while anything else (a submodule, most notably) keeps its own directory
// as the root, because it genuinely is a separate repository.
func rootFromGitFile(dir, gitFile string) (string, error) {
	gitDir, err := parseGitFile(gitFile)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	if main, ok := mainRepoFromWorktreeGitDir(filepath.Clean(gitDir)); ok {
		return main, nil
	}
	return dir, nil
}

// parseGitFile reads the `gitdir: <path>` line out of a `.git` file.
func parseGitFile(gitFile string) (string, error) {
	f, err := os.Open(gitFile)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", gitFile, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if rest, ok := strings.CutPrefix(line, "gitdir:"); ok {
			gitDir := strings.TrimSpace(rest)
			if gitDir == "" {
				return "", fmt.Errorf("%s: empty gitdir", gitFile)
			}
			return gitDir, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read %s: %w", gitFile, err)
	}
	return "", fmt.Errorf("%s: no gitdir line", gitFile)
}

// mainRepoFromWorktreeGitDir maps a linked worktree's git dir back to the main
// repo root: `/ws/todo/.git/worktrees/todo-scope` -> `/ws/todo`.
//
// The segment before `worktrees` must be `.git`, so a repository that merely
// happens to live in a directory called `worktrees` is not mistaken for one.
func mainRepoFromWorktreeGitDir(gitDir string) (string, bool) {
	sep := string(filepath.Separator)
	idx := strings.LastIndex(gitDir, sep+"worktrees"+sep)
	if idx < 0 {
		return "", false
	}
	commonDir := gitDir[:idx] // .../<repo>/.git
	if filepath.Base(commonDir) != ".git" {
		return "", false
	}
	return filepath.Dir(commonDir), true
}

// normalizePath makes a path absolute, symlink-free and clean. On macOS this is
// what collapses /tmp and /private/tmp into one key.
func normalizePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// A deleted or unreadable directory must not become a task key.
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	return filepath.Clean(resolved), nil
}
