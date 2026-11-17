package gitrepo

import (
	"bufio"
	"bytes"
	"context"
	"strconv"
	"strings"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/diff"
)

// Attribute fills in Line.Commit for every added and removed line in files:
// the PR commit that added the line, or the one that removed it.
// Lines that came from outside the PR (before base) are left empty.
func (r *Repo) Attribute(ctx context.Context, base, head string, files []diff.File) error {
	children, err := r.children(ctx, base, head)
	if err != nil {
		return err
	}
	for i := range files {
		f := &files[i]
		if f.Binary {
			continue
		}
		var added, removed bool
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				added = added || l.Kind == diff.Added
				removed = removed || l.Kind == diff.Removed
			}
		}
		var addedBy, lastHad map[int]string
		if added {
			if addedBy, err = r.blame(ctx, base, head, f.Path, false); err != nil {
				return err
			}
		}
		if removed {
			if lastHad, err = r.blame(ctx, base, head, f.OldPath, true); err != nil {
				return err
			}
		}
		for hi := range f.Hunks {
			lines := f.Hunks[hi].Lines
			for li := range lines {
				l := &lines[li]
				switch l.Kind {
				case diff.Added:
					l.Commit = addedBy[l.New]
				case diff.Removed:
					// Reverse blame names the last commit that still had the
					// line; the one after it removed it.
					l.Commit = firstChild(children, lastHad[l.Old])
				}
			}
		}
	}
	return nil
}

// blame maps each line number of the file to the PR commit responsible.
// Forward: the file at head, the commit that added each line.
// Reverse: the file at base, the last commit in the range that still had each line.
// Lines from outside the range map to "".
func (r *Repo) blame(ctx context.Context, base, head, path string, reverse bool) (map[int]string, error) {
	args := []string{"blame", "--porcelain"}
	if reverse {
		args = append(args, "--reverse")
	}
	args = append(args, base+".."+head, "--", path)
	out, err := r.git(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseBlame(out, base, reverse), nil
}

func parseBlame(out []byte, base string, reverse bool) map[int]string {
	res := map[int]string{}
	boundary := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	var sha string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "\t") {
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 3 && len(f[0]) == 40 && isHex(f[0]) {
			sha = f[0]
			n, _ := strconv.Atoi(f[2])
			res[n] = sha
			continue
		}
		if line == "boundary" {
			boundary[sha] = true
		}
	}
	for n, s := range res {
		if reverse {
			// In reverse mode the boundary is base itself; a line whose last
			// home was base was removed by base's first child.
			if boundary[s] {
				res[n] = base
			}
			continue
		}
		if boundary[s] || s == base {
			res[n] = ""
		}
	}
	return res
}

// children maps each commit in base..head (and base itself) to its children
// within that range, in commit order.
func (r *Repo) children(ctx context.Context, base, head string) (map[string][]string, error) {
	out, err := r.git(ctx, "rev-list", "--reverse", "--topo-order", "--parents", base+".."+head)
	if err != nil {
		return nil, err
	}
	ch := map[string][]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		for _, p := range f[1:] {
			ch[p] = append(ch[p], f[0])
		}
	}
	return ch, nil
}

func firstChild(children map[string][]string, sha string) string {
	if sha == "" {
		return ""
	}
	if c := children[sha]; len(c) > 0 {
		return c[0]
	}
	return ""
}

func isHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
