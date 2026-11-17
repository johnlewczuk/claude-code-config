// Package gitrepo keeps a local partial clone of a GitHub repo and answers
// questions about a PR from it: the diff, file contents, and which PR commit
// added or removed each line.
package gitrepo

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/diff"
)

// Repo is a bare partial clone under the cache folder.
type Repo struct {
	Dir   string // the bare repo
	URL   string // clone URL; empty for a repo that already exists (tests)
	ghBin string

	mu sync.Mutex // one git fetch/clone at a time per repo
}

// Open returns the repo for owner/name under cacheDir. It doesn't touch disk.
func Open(cacheDir, owner, name string) *Repo {
	gh, _ := exec.LookPath("gh")
	return &Repo{
		Dir:   filepath.Join(cacheDir, "repos", owner, name+".git"),
		URL:   fmt.Sprintf("https://github.com/%s/%s.git", owner, name),
		ghBin: gh,
	}
}

// Progress receives git's progress lines ("Receiving objects: 45% ...").
type Progress func(string)

func (r *Repo) authArgs() []string {
	if r.ghBin == "" {
		return nil
	}
	// Use the gh login for https, the same way `gh auth setup-git` does.
	return []string{"-c", "credential.helper=", "-c", "credential.helper=!" + r.ghBin + " auth git-credential"}
}

func (r *Repo) git(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"-C", r.Dir, "-c", "core.quotepath=off"}, r.authArgs()...)
	cmd := exec.CommandContext(ctx, "git", append(full, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// gitProgress runs a git command whose progress goes to stderr and passes
// each progress line to p.
func (r *Repo) gitProgress(ctx context.Context, p Progress, dir string, args ...string) error {
	full := append([]string{"-c", "core.quotepath=off"}, r.authArgs()...)
	if dir != "" {
		full = append([]string{"-C", dir}, full...)
	}
	cmd := exec.CommandContext(ctx, "git", append(full, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var tail []string
	sc := bufio.NewScanner(stderr)
	sc.Split(scanProgress)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if p != nil {
			p(line)
		}
		tail = append(tail, line)
		if len(tail) > 5 {
			tail = tail[1:]
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.Join(tail, " / "))
	}
	return nil
}

// scanProgress splits on \n or \r, since git redraws progress with \r.
func scanProgress(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// Fetch makes sure the repo exists and has the PR head and the base branch.
// The first call for a repo clones it, which can be slow for a big repo.
func (r *Repo) Fetch(ctx context.Context, number int, baseRef string, p Progress) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := os.Stat(filepath.Join(r.Dir, "HEAD")); err != nil {
		if err := os.MkdirAll(filepath.Dir(r.Dir), 0o755); err != nil {
			return err
		}
		if p != nil {
			p("Copying the repo for the first time (later opens are fast)")
		}
		tmp := r.Dir + ".partial"
		os.RemoveAll(tmp)
		if err := r.gitProgress(ctx, p, "", "clone", "--bare", "--filter=blob:none", "--no-tags", "--progress", r.URL, tmp); err != nil {
			return err
		}
		if err := os.Rename(tmp, r.Dir); err != nil {
			return err
		}
	}
	if p != nil {
		p("Fetching the PR")
	}
	return r.gitProgress(ctx, p, r.Dir, "fetch", "--no-tags", "--progress", "--filter=blob:none", "origin",
		fmt.Sprintf("+refs/pull/%d/head:refs/prview/pull/%d", number, number),
		fmt.Sprintf("+refs/heads/%s:refs/prview/base/%s", baseRef, baseRef))
}

// MergeBase returns the commit GitHub diffs a PR against.
func (r *Repo) MergeBase(ctx context.Context, a, b string) (string, error) {
	out, err := r.git(ctx, "merge-base", a, b)
	return strings.TrimSpace(string(out)), err
}

// PrefetchBlobs downloads, in one go, the file contents the diff and blame
// will need, so a partial clone doesn't fetch them one at a time.
func (r *Repo) PrefetchBlobs(ctx context.Context, base, head string, paths []string) error {
	if r.URL == "" {
		return nil
	}
	var missing []string
	// Blobs the PR's commits introduced.
	out, err := r.git(ctx, "rev-list", "--objects", "--missing=print", base+".."+head)
	if err != nil {
		return err
	}
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "?") {
			missing = append(missing, strings.TrimPrefix(l, "?"))
		}
	}
	// The base versions of the changed files.
	if len(paths) > 0 {
		args := append([]string{"ls-tree", "-r", base, "--"}, paths...)
		out, err = r.git(ctx, args...)
		if err != nil {
			return err
		}
		var oids []string
		for _, l := range strings.Split(string(out), "\n") {
			f := strings.Fields(l)
			if len(f) >= 3 && f[1] == "blob" {
				oids = append(oids, f[2])
			}
		}
		if len(oids) > 0 {
			cmd := exec.CommandContext(ctx, "git", "-C", r.Dir, "cat-file", "--batch-check=%(objectname)", "--buffer")
			cmd.Stdin = strings.NewReader(strings.Join(oids, "\n") + "\n")
			res, _ := cmd.Output() // missing objects print "<oid> missing"
			for _, l := range strings.Split(string(res), "\n") {
				if strings.HasSuffix(l, " missing") {
					missing = append(missing, strings.TrimSuffix(l, " missing"))
				}
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	full := append([]string{"-C", r.Dir}, r.authArgs()...)
	full = append(full, "fetch", "--no-tags", "--no-write-fetch-head", "--filter=blob:none", "--stdin", "origin")
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.Stdin = strings.NewReader(strings.Join(missing, "\n") + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("fetching file contents: %w: %s", err, out)
	}
	return nil
}

// ChangedPaths lists the paths changed between base and head, old and new
// names both, without needing file contents (rename detection is skipped).
func (r *Repo) ChangedPaths(ctx context.Context, base, head string) ([]string, error) {
	out, err := r.git(ctx, "diff", "--name-only", "--no-renames", base, head)
	if err != nil {
		return nil, err
	}
	return strings.Fields(strings.TrimSpace(string(out))), nil
}

// Diff returns the diff between base and head, like GitHub's PR diff.
func (r *Repo) Diff(ctx context.Context, base, head string) ([]diff.File, error) {
	out, err := r.git(ctx, "diff", "--no-color", "--no-ext-diff", "-M", "--unified=3", base, head)
	if err != nil {
		return nil, err
	}
	return diff.Parse(bytes.NewReader(out))
}

// FileLines returns the lines of path at rev.
func (r *Repo) FileLines(ctx context.Context, rev, path string) ([]string, error) {
	out, err := r.git(ctx, "show", rev+":"+path)
	if err != nil {
		return nil, err
	}
	s := strings.TrimSuffix(string(out), "\n")
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

// HasCommit reports whether rev is in the repo.
func (r *Repo) HasCommit(ctx context.Context, rev string) bool {
	_, err := r.git(ctx, "cat-file", "-e", rev+"^{commit}")
	return err == nil
}

// Worktree checks out head into dir, reusing the folder across heads so a
// Claude conversation that started there can continue.
func (r *Repo) Worktree(ctx context.Context, dir, head string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		cmd := exec.CommandContext(ctx, "git", append(append([]string{"-C", dir}, r.authArgs()...), "checkout", "--quiet", "--detach", "--force", head)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git checkout: %w: %s", err, out)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	r.git(ctx, "worktree", "prune")
	_, err := r.git(ctx, "worktree", "add", "--detach", "--force", dir, head)
	return err
}
