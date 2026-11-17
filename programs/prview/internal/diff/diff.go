// Package diff reads the output of `git diff` into files, hunks and lines.
package diff

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Kind says whether a line is unchanged, added or removed.
type Kind string

const (
	Context Kind = "ctx"
	Added   Kind = "add"
	Removed Kind = "del"
)

// Line is one line of a hunk.
type Line struct {
	Kind Kind `json:"kind"`
	// Old and New are 1-based line numbers; 0 when the line has no number on that side.
	Old  int    `json:"old,omitempty"`
	New  int    `json:"new,omitempty"`
	Text string `json:"text"`
	// Commit is the PR commit that added (or, for a removed line, removed) this line.
	Commit string `json:"commit,omitempty"`
}

// Hunk is one `@@` block.
type Hunk struct {
	OldStart int    `json:"oldStart"`
	OldLines int    `json:"oldLines"`
	NewStart int    `json:"newStart"`
	NewLines int    `json:"newLines"`
	Header   string `json:"header"`
	Lines    []Line `json:"lines"`
}

// File is the diff of one file.
type File struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath"`
	Status    string `json:"status"` // added, deleted, modified, renamed
	Binary    bool   `json:"binary"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Hunks     []Hunk `json:"hunks"`
}

// Parse reads unified diff text produced by `git diff` (with a/ and b/ prefixes).
func Parse(r io.Reader) ([]File, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)

	var files []File
	var cur *File
	var hunk *Hunk
	oldNo, newNo := 0, 0

	flushHunk := func() {
		if cur != nil && hunk != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
		}
		hunk = nil
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			if cur.Path == "" {
				cur.Path = cur.OldPath
			}
			if cur.OldPath == "" {
				cur.OldPath = cur.Path
			}
			if cur.Status == "" {
				cur.Status = "modified"
			}
			files = append(files, *cur)
		}
		cur = nil
	}

	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "diff --git ") {
			flushFile()
			cur = &File{}
			a, b := splitGitHeader(strings.TrimPrefix(line, "diff --git "))
			cur.OldPath, cur.Path = a, b
			continue
		}
		if cur == nil {
			continue
		}
		if hunk != nil {
			if line == "" {
				// A blank context line can come through without its leading space.
				line = " "
			}
			switch line[0] {
			case ' ':
				oldNo++
				newNo++
				hunk.Lines = append(hunk.Lines, Line{Kind: Context, Old: oldNo, New: newNo, Text: line[1:]})
				continue
			case '+':
				newNo++
				cur.Additions++
				hunk.Lines = append(hunk.Lines, Line{Kind: Added, New: newNo, Text: line[1:]})
				continue
			case '-':
				oldNo++
				cur.Deletions++
				hunk.Lines = append(hunk.Lines, Line{Kind: Removed, Old: oldNo, Text: line[1:]})
				continue
			case '\\':
				continue // "\ No newline at end of file"
			}
			flushHunk()
		}
		switch {
		case strings.HasPrefix(line, "@@ "):
			h, err := parseHunkHeader(line)
			if err != nil {
				return nil, err
			}
			hunk = &h
			oldNo, newNo = h.OldStart-1, h.NewStart-1
		case strings.HasPrefix(line, "--- "):
			if p := strings.TrimPrefix(line, "--- "); p == "/dev/null" {
				cur.Status = "added"
			} else {
				cur.OldPath = strings.TrimPrefix(unquote(p), "a/")
			}
		case strings.HasPrefix(line, "+++ "):
			if p := strings.TrimPrefix(line, "+++ "); p == "/dev/null" {
				cur.Status = "deleted"
				cur.Path = cur.OldPath
			} else {
				cur.Path = strings.TrimPrefix(unquote(p), "b/")
			}
		case strings.HasPrefix(line, "new file mode"):
			cur.Status = "added"
		case strings.HasPrefix(line, "deleted file mode"):
			cur.Status = "deleted"
		case strings.HasPrefix(line, "rename from "):
			cur.OldPath = unquote(strings.TrimPrefix(line, "rename from "))
			cur.Status = "renamed"
		case strings.HasPrefix(line, "rename to "):
			cur.Path = unquote(strings.TrimPrefix(line, "rename to "))
			cur.Status = "renamed"
		case strings.HasPrefix(line, "Binary files "):
			cur.Binary = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	flushFile()
	return files, nil
}

func parseHunkHeader(line string) (Hunk, error) {
	// @@ -a,b +c,d @@ header
	end := strings.Index(line[3:], " @@")
	if end < 0 {
		return Hunk{}, fmt.Errorf("bad hunk header %q", line)
	}
	ranges := strings.Fields(line[3 : 3+end])
	if len(ranges) != 2 {
		return Hunk{}, fmt.Errorf("bad hunk header %q", line)
	}
	var h Hunk
	h.OldStart, h.OldLines = parseRange(strings.TrimPrefix(ranges[0], "-"))
	h.NewStart, h.NewLines = parseRange(strings.TrimPrefix(ranges[1], "+"))
	h.Header = strings.TrimSpace(line[3+end+3:])
	return h, nil
}

func parseRange(s string) (start, count int) {
	count = 1
	if i := strings.IndexByte(s, ','); i >= 0 {
		count, _ = strconv.Atoi(s[i+1:])
		s = s[:i]
	}
	start, _ = strconv.Atoi(s)
	return start, count
}

// splitGitHeader splits "a/x b/y" from a `diff --git` line. When the two
// paths are the same (no rename) the split is exact even if paths contain
// spaces; renames are corrected later from the "rename from/to" lines.
func splitGitHeader(s string) (string, string) {
	if strings.HasPrefix(s, `"`) {
		parts := splitQuoted(s)
		if len(parts) == 2 {
			return strings.TrimPrefix(parts[0], "a/"), strings.TrimPrefix(parts[1], "b/")
		}
	}
	if n := len(s); n%2 == 1 {
		half := (n - 1) / 2
		a, b := s[:half], s[half+1:]
		if strings.TrimPrefix(a, "a/") == strings.TrimPrefix(b, "b/") {
			return strings.TrimPrefix(a, "a/"), strings.TrimPrefix(b, "b/")
		}
	}
	if i := strings.Index(s, " b/"); i >= 0 {
		return strings.TrimPrefix(s[:i], "a/"), s[i+3:]
	}
	return s, s
}

func splitQuoted(s string) []string {
	var out []string
	for len(s) > 0 {
		s = strings.TrimLeft(s, " ")
		if s == "" {
			break
		}
		if s[0] == '"' {
			q, err := strconv.QuotedPrefix(s)
			if err != nil {
				return nil
			}
			u, _ := strconv.Unquote(q)
			out = append(out, u)
			s = s[len(q):]
		} else {
			i := strings.IndexByte(s, ' ')
			if i < 0 {
				out = append(out, s)
				break
			}
			out = append(out, s[:i])
			s = s[i:]
		}
	}
	return out
}

func unquote(p string) string {
	p = strings.TrimSuffix(p, "\t")
	if strings.HasPrefix(p, `"`) {
		if u, err := strconv.Unquote(p); err == nil {
			return u
		}
	}
	return p
}
