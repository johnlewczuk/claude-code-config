package order

import (
	"fmt"
	"strings"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/diff"
)

// Numbered writes the diff with explicit old and new line numbers on every
// line, so Claude can name exact line ranges. Output stops at about max
// bytes; files past that are listed by name only.
func Numbered(files []diff.File, max int) string {
	var b strings.Builder
	var skipped []string
	for _, f := range files {
		if b.Len() > max {
			skipped = append(skipped, f.Path)
			continue
		}
		name := f.Path
		if f.Status == "renamed" {
			name = f.OldPath + " -> " + f.Path
		}
		fmt.Fprintf(&b, "=== %s (%s, +%d -%d)\n", name, f.Status, f.Additions, f.Deletions)
		if f.Binary {
			b.WriteString("(binary file)\n\n")
			continue
		}
		for _, h := range f.Hunks {
			fmt.Fprintf(&b, "@@ %s\n", h.Header)
			for _, l := range h.Lines {
				old, nw := "", ""
				if l.Old > 0 {
					old = fmt.Sprint(l.Old)
				}
				if l.New > 0 {
					nw = fmt.Sprint(l.New)
				}
				mark := " "
				switch l.Kind {
				case diff.Added:
					mark = "+"
				case diff.Removed:
					mark = "-"
				}
				fmt.Fprintf(&b, "%5s %5s %s %s\n", old, nw, mark, l.Text)
			}
		}
		b.WriteString("\n")
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&b, "(diff too long; these files are changed but not shown: %s)\n", strings.Join(skipped, ", "))
	}
	return b.String()
}

// Clean drops steps that don't point at a file in the diff and keeps line
// numbers in order.
func (o *Order) Clean(files []diff.File) {
	known := map[string]bool{}
	for _, f := range files {
		known[f.Path] = true
		known[f.OldPath] = true
	}
	steps := o.Steps[:0]
	for _, s := range o.Steps {
		if !known[s.File] {
			continue
		}
		if s.Side != "old" {
			s.Side = "new"
		}
		if s.EndLine < s.StartLine {
			s.EndLine = s.StartLine
		}
		steps = append(steps, s)
	}
	o.Steps = steps
	for i := range o.Groups {
		g := &o.Groups[i]
		fs := g.Files[:0]
		for _, f := range g.Files {
			if known[f] {
				fs = append(fs, f)
			}
		}
		g.Files = fs
	}
}
