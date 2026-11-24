// Package summary builds the questions asked of Claude for file and section
// summaries, decides which files are not worth asking about, and keeps the
// finished answers on disk.
package summary

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/code"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/diff"
)

// Schema is the shape of Claude's answer, for both kinds of summary.
const Schema = `{"type":"object","additionalProperties":false,"properties":{"summary":{"type":"string"}},"required":["summary"]}`

// FileSystem is the system prompt for a whole-file summary.
const FileSystem = `You summarize one file of a GitHub pull request for the person reviewing it.
Write at a middle level of detail: 3 to 6 sentences. Name the functions and types involved and say how they connect. If the PR description says why, say what the PR changed in this file and why. Do not go line by line.
Use plain words. No filler, no headings, no bullet points, no opening like "This file".`

// SectionSystem is the system prompt for a summary of one section of code.
const SectionSystem = `You explain one section of code (a function, a type, or a few selected lines) to the person reviewing a GitHub pull request.
Write at a low level of detail: say what the code does, step by step. Then say what this PR changed in it, or write "Not changed by this PR." if nothing in it was changed.
Keep it to a few short sentences or a short list. Use plain words. No filler, no headings.`

const (
	maxDiffChars = 60000
	maxFileChars = 60000
	maxBodyChars = 4000
	maxUses      = 20
)

var lockFiles = map[string]bool{
	"go.sum": true, "package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true,
	"Cargo.lock": true, "poetry.lock": true, "composer.lock": true, "Gemfile.lock": true,
}

var generatedSuffixes = []string{".pb.go", "_pb2.py", ".min.js", ".min.css"}

// Skip returns why a file is not worth summarizing, or "" if it is.
func Skip(f diff.File) string {
	if f.Binary {
		return "binary file"
	}
	base := path.Base(f.Path)
	if lockFiles[base] {
		return "lock file"
	}
	for _, s := range generatedSuffixes {
		if strings.HasSuffix(base, s) {
			return "generated file"
		}
	}
	for _, dir := range strings.Split(path.Dir(f.Path), "/") {
		if dir == "vendor" || dir == "node_modules" {
			return "vendored code"
		}
	}
	return ""
}

// DiffText is the file's changes as unified diff text.
func DiffText(f diff.File) string {
	var b strings.Builder
	for _, h := range f.Hunks {
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		if h.Header != "" {
			b.WriteString(" " + h.Header)
		}
		b.WriteByte('\n')
		for _, l := range h.Lines {
			b.WriteString(markOf(l.Kind) + l.Text + "\n")
		}
	}
	return b.String()
}

func markOf(k diff.Kind) string {
	switch k {
	case diff.Added:
		return "+"
	case diff.Removed:
		return "-"
	}
	return " "
}

// cut keeps whole lines of text up to limit characters. If it drops any, it
// ends with a note saying how many lines are missing.
func cut(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	lines := strings.SplitAfter(text, "\n")
	used, n := 0, 0
	for _, l := range lines {
		used += utf8.RuneCountInString(l)
		if used > limit {
			break
		}
		n++
	}
	return strings.Join(lines[:n], "") + fmt.Sprintf("\n…cut here, %d more lines\n", len(lines)-n)
}

// FilePrompt is the question for a whole-file summary. fileText is the file's
// lines after the PR (nil when the file was deleted).
func FilePrompt(title, body string, f diff.File, fileText []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Summarize this file from a pull request.\n\nFile: %s (%s)\nPR title: %s\n", f.Path, f.Status, title)
	if strings.TrimSpace(body) != "" {
		b.WriteString("\nPR description, for what the author meant to do:\n" + cut(body, maxBodyChars) + "\n")
	}
	b.WriteString("\nWhat the PR changed in this file, as a diff (lines starting with + were added, lines starting with - were removed):\n" + cut(DiffText(f), maxDiffChars))
	if len(fileText) > 0 {
		b.WriteString("\nThe whole file after the PR:\n" + cut(strings.Join(fileText, "\n")+"\n", maxFileChars))
	}
	b.WriteString("\nAnswer in plain words, with no headings.")
	return b.String()
}

// MarkedLine is one line of a section. Mark is '+' if the PR added the line,
// '-' if it removed it, or ' ' if it is unchanged.
type MarkedLine struct {
	Mark byte
	Text string
}

// SectionInput is what the question about one section needs.
type SectionInput struct {
	Path, Name  string // Name is empty for a selection
	Lines       []MarkedLine
	Uses        []code.Place
	FileSummary string // empty if there is none yet
}

// MarkSection returns lines start..end (1-based) of text, marked with what the
// PR did. side is "new" (text is the file after the PR) or "old" (text is the
// file before). f is nil when the PR doesn't touch the file.
func MarkSection(f *diff.File, side string, text []string, start, end int) []MarkedLine {
	added := map[int]bool{}
	removedNew := map[int][]string{} // removed lines, keyed by the next new line after them
	removedOld := map[int]bool{}
	if f != nil {
		for _, h := range f.Hunks {
			var pending []string
			for _, l := range h.Lines {
				switch l.Kind {
				case diff.Removed:
					removedOld[l.Old] = true
					pending = append(pending, l.Text)
				case diff.Added:
					added[l.New] = true
					fallthrough
				default:
					if l.New > 0 && len(pending) > 0 {
						removedNew[l.New] = append(removedNew[l.New], pending...)
						pending = nil
					}
				}
			}
		}
	}
	var out []MarkedLine
	for n := start; n <= end && n <= len(text); n++ {
		if n < 1 {
			continue
		}
		if side == "old" {
			mark := byte(' ')
			if removedOld[n] {
				mark = '-'
			}
			out = append(out, MarkedLine{mark, text[n-1]})
			continue
		}
		for _, t := range removedNew[n] {
			out = append(out, MarkedLine{'-', t})
		}
		mark := byte(' ')
		if added[n] {
			mark = '+'
		}
		out = append(out, MarkedLine{mark, text[n-1]})
	}
	return out
}

func markedText(lines []MarkedLine) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteByte(l.Mark)
		b.WriteString(l.Text + "\n")
	}
	return b.String()
}

// SectionPrompt is the question for a section summary.
func SectionPrompt(in SectionInput) string {
	var b strings.Builder
	if in.Name != "" {
		fmt.Fprintf(&b, "Explain %s, in the file %s.\n", in.Name, in.Path)
	} else {
		fmt.Fprintf(&b, "Explain the selected lines in the file %s.\n", in.Path)
	}
	b.WriteString("\nThe code follows. A line starting with + was added by the PR, a line starting with - was removed by the PR, and a line starting with a space is unchanged:\n\n")
	b.WriteString(markedText(in.Lines))
	if len(in.Uses) > 0 {
		b.WriteString("\nUsed in:\n")
		for i, u := range in.Uses {
			if i == maxUses {
				fmt.Fprintf(&b, "…and %d more places\n", len(in.Uses)-maxUses)
				break
			}
			fmt.Fprintf(&b, "%s:%d: %s\n", u.Path, u.Line, strings.TrimSpace(u.Text))
		}
	}
	if in.FileSummary != "" {
		b.WriteString("\nWhat the whole file does, for context:\n" + in.FileSummary + "\n")
	}
	b.WriteString("\nAnswer in plain words, with no headings.")
	return b.String()
}

func key(p, text string) string {
	h := sha256.Sum256([]byte(p + "\x00" + text))
	return hex.EncodeToString(h[:])
}

// FileKey names the saved summary of a file's changes.
func FileKey(f diff.File) string { return key(f.Path, DiffText(f)) }

// SectionKey names the saved summary of a marked section.
func SectionKey(p string, lines []MarkedLine) string { return key(p, markedText(lines)) }

// Store keeps finished summaries as files: <Dir>/<kind>/<key>.json.
type Store struct{ Dir string }

// Get returns the saved summary, if there is one.
func (s Store) Get(kind, key string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(s.Dir, kind, key+".json"))
	if err != nil {
		return "", false
	}
	var v struct {
		Summary string `json:"summary"`
	}
	if json.Unmarshal(b, &v) != nil || v.Summary == "" {
		return "", false
	}
	return v.Summary, true
}

// Put saves a summary.
func (s Store) Put(kind, key, text string) error {
	dir := filepath.Join(s.Dir, kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(map[string]string{"summary": text})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, key+".json"), b, 0o644)
}
