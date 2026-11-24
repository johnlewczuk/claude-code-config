// Package order works out the order to read a PR in: from the PR
// description's "Suggested reading order" if it has one, otherwise written
// by Claude.
package order

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/code"
)

// Step is one place to read.
type Step struct {
	File      string `json:"file"`
	Function  string `json:"function"`
	Note      string `json:"note"`
	Side      string `json:"side"` // "new" or "old": which side of the diff the lines are on
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
}

// Group is a set of files with mechanical edits, read as one.
type Group struct {
	Note  string   `json:"note"`
	Files []string `json:"files"`
}

// Order is a reading order.
type Order struct {
	Source string  `json:"source"` // "description" or "claude"
	Head   string  `json:"head"`   // the head commit it was written for
	Steps  []Step  `json:"steps"`
	Groups []Group `json:"groups"`
}

var (
	headingRe = regexp.MustCompile(`(?i)suggested\s+reading\s+order`)
	itemRe    = regexp.MustCompile(`^\s*\d+[.)]\s+(.*)$`)
	// A file path: has a dot or a slash, no spaces.
	pathRe = regexp.MustCompile(`^[A-Za-z0-9_./+@-]*[./][A-Za-z0-9_./+@-]*$`)
)

// FromDescription reads a "Suggested reading order" numbered list from a PR
// description. It returns nil when there isn't one.
func FromDescription(body string) *Order {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	start := -1
	for i, l := range lines {
		if headingRe.MatchString(l) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil
	}
	o := &Order{Source: "description"}
	seenItem := false
	for _, l := range lines[start:] {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") || (seenItem && t != "" && !itemRe.MatchString(l) && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t")) {
			break
		}
		m := itemRe.FindStringSubmatch(l)
		if m == nil {
			if seenItem && t != "" && len(o.Steps) > 0 {
				// A wrapped note continues the previous item.
				s := &o.Steps[len(o.Steps)-1]
				s.Note = strings.TrimSpace(s.Note + " " + t)
			}
			continue
		}
		seenItem = true
		if s, ok := parseItem(m[1]); ok {
			o.Steps = append(o.Steps, s)
		}
	}
	if len(o.Steps) == 0 {
		return nil
	}
	return o
}

func parseItem(text string) (Step, bool) {
	text = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "**"))
	// The reference is what's inside the first backticks, or the first word.
	var ref, rest string
	if strings.HasPrefix(text, "`") {
		end := strings.Index(text[1:], "`")
		if end < 0 {
			return Step{}, false
		}
		ref, rest = text[1:1+end], text[2+end:]
	} else {
		ref, rest, _ = strings.Cut(text, " ")
		ref = strings.TrimRight(ref, ":,")
	}
	file, fn, _ := strings.Cut(ref, ":")
	if !pathRe.MatchString(file) {
		return Step{}, false
	}
	// Drop the separator between the reference and the note: "—", ":",
	// ", then …", and closing bold.
	note := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(rest), "*—–-:,; "))
	return Step{File: file, Function: strings.TrimSpace(fn), Note: note, Side: "new"}, true
}

// Locate finds the line where function fn is defined in a file's lines.
// It returns 0 when it can't find it. fn may be "Type::Method", "Type.Method"
// or a plain name.
func Locate(lines []string, fn string) int {
	fn = strings.TrimSuffix(strings.TrimSpace(fn), "()")
	if fn == "" {
		return 0
	}
	names := []string{fn}
	for _, sep := range []string{"::", "."} {
		if i := strings.LastIndex(fn, sep); i >= 0 {
			names = append(names, fn[i+len(sep):])
		}
	}
	for _, name := range names {
		word := regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(name) + `\s*[(<{:=]?`)
		best := 0
		for i, l := range lines {
			if !word.MatchString(l) {
				continue
			}
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "*") {
				continue
			}
			if code.IsDefinition(l, name) {
				return i + 1
			}
			if best == 0 {
				best = i + 1
			}
		}
		if best > 0 && name == names[len(names)-1] {
			return best
		}
	}
	return 0
}

// Schema is the JSON shape Claude must answer in.
const Schema = `{"type":"object","additionalProperties":false,
"properties":{
 "steps":{"type":"array","items":{"type":"object","additionalProperties":false,
   "properties":{"file":{"type":"string"},"function":{"type":"string"},"note":{"type":"string"},
     "side":{"type":"string","enum":["new","old"]},"startLine":{"type":"integer"},"endLine":{"type":"integer"}},
   "required":["file","function","note","side","startLine","endLine"]}},
 "groups":{"type":"array","items":{"type":"object","additionalProperties":false,
   "properties":{"note":{"type":"string"},"files":{"type":"array","items":{"type":"string"}}},
   "required":["note","files"]}}},
"required":["steps","groups"]}`

// System is the role Claude plays when writing a reading order.
const System = `You write the order in which a reviewer should read a pull request's changes.

Rules:
- Start with the entrypoints: where the new behaviour begins at runtime (a request handler, a reconcile loop, a CLI command, a main loop, a node's Process). Use more than one when the change has independent triggers.
- Then follow the call path outward, in the order the code calls things.
- Put helpers, pure functions, data types and tests last.
- Each step is one function (or one coherent block) in one file, with a one-line note on what to look for there. Notes are short and in plain words.
- startLine and endLine are line numbers from the numbered diff you are given, on the side you name ("new" for added or unchanged lines, "old" for lines that only exist in the old file). They must point at changed lines of that step.
- Mechanical edits (a rename, a signature change repeated across files, formatting, generated files, lockfiles) are not steps: put them in groups, one group per kind of edit, with a one-line note.
- Every file in the diff should be in a step or a group if it matters for review; files you leave out are listed separately to the reviewer.`

// Prompt builds the question for Claude from the PR and its numbered diff.
func Prompt(title, body, numberedDiff string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Pull request: %s\n\n", title)
	if strings.TrimSpace(body) != "" {
		fmt.Fprintf(&b, "Description:\n%s\n\n", body)
	}
	b.WriteString("Numbered diff. Each line is: old line number, new line number, then + (added), - (removed) or a space (unchanged), then the code.\n\n")
	b.WriteString(numberedDiff)
	return b.String()
}
