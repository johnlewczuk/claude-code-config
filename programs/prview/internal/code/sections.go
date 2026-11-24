package code

import (
	"regexp"
	"strings"
)

// DefinedName returns the name the line defines, or "" when it defines
// nothing. See IsDefinition for what counts.
func DefinedName(line string) string {
	name, _ := define(line)
	return name
}

// define is DefinedName that also says whether what it defines is a plain
// value (a constant, variable or field) rather than a function or type.
func define(line string) (name string, value bool) {
	rest := strings.TrimLeft(line, " \t")
	indented := len(rest) < len(line)
	skipped := false // a modifier or a definition word came first
	skip := func(re *regexp.Regexp) []string {
		m := re.FindStringSubmatch(rest)
		if m != nil {
			rest = rest[len(m[0]):]
			skipped = true
		}
		return m
	}
	skip(modifiers)
	w := skip(defWord)
	if w == nil {
		n, isCall := declaredName(rest, indented, skipped, line)
		return n, !isCall
	}
	skip(receiver)
	skip(typeParams)
	skip(modifiers)
	// func Run, class Foo, let x, #define MAX: the first name. After a word
	// that can also begin a C type, the declarator is tried first:
	// "const std::string& name()" defines name, not string.
	isValueWord := w[1] == "let" || w[1] == "var" || w[1] == "val" || w[1] == "const"
	if cTypeWords[w[1]] {
		if n, isCall := declaredName(rest, indented, skipped, line); n != "" {
			return n, w[1] == "const" && !isCall
		}
	}
	if m := firstName.FindStringSubmatch(rest); m != nil {
		return m[1], isValueWord
	}
	return "", false
}

// declaredName reads a C and C++ style declaration: a type, then the name.
// isCall says the name is followed by "(".
func declaredName(rest string, indented, skipped bool, line string) (name string, isCall bool) {
	// A type, then the name: int helper(), void Node::Run(), public
	// getUser(), static const int kMax = 5.
	m := declarator.FindStringSubmatch(rest)
	if m == nil {
		return "", false
	}
	for _, w := range words.FindAllString(m[1], -1) {
		if statementWords[w] {
			return "", false
		}
	}
	isCall = m[3] == "("
	switch {
	case skipped:
		return m[2], isCall
	case !isCall:
		// "x, y = f()" and "err := f()" assign; only a modifier or a
		// definition word makes these a declaration.
		return "", false
	case m[1] != "":
		return m[2], true
	}
	// No type: a constructor (Foo::Foo(...)) or a function whose return type
	// is on the line above, at the start of a line not ending in ";".
	if !indented && !strings.HasSuffix(strings.TrimSpace(line), ";") {
		return m[2], true
	}
	return "", false
}

// Section is a run of lines of a file: its top, or one function or type
// with the comment above it. Lines are 1-based and Start..End is inclusive.
type Section struct {
	Name  string `json:"name"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

var typeOpener = regexp.MustCompile(`^(?:(?:pub(?:\([^)]*\))?|export|default|public|private|protected|internal|abstract|final|static|declare)\s+)*(?:class|struct|interface|impl|trait|enum|namespace|module|type\s+\w+(?:<[^>]*>)?\s+(?:struct|interface))\b`)

// method matches a method inside a type body that has no definition word and
// no return type in front, as in TypeScript's "load(): void {".
var method = regexp.MustCompile(`^(?:(?:public|private|protected|static|async|override|readonly|get|set)\s+)*(\w+)\s*\([^)]*\)[^;=]*\{\s*$`)

// arrowFunc matches a constant or variable at the left edge that holds a
// function, as in "export const g = (a: number) => {".
var arrowFunc = regexp.MustCompile(`^(?:export\s+(?:default\s+)?)?(?:const|let|var)\s+\w+[^=]*=\s*(?:async\s+)?(?:function\b|\([^)]*\)\s*(?::[^=]+)?=>|\w+\s*=>)`)

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// Sections splits a file into the top of the file (Name "", when it holds
// anything but blank lines) and one section per function or type. A section
// starts at the comment directly above its definition and runs to the line
// before the next section, minus trailing blank lines. Definitions at the
// left edge always start a section; indented ones only when they are direct
// members of a type (class, struct, interface, impl, trait or enum), so
// locals inside a function don't. Constants, variables and fields never do, except those holding a function.
func Sections(path string, lines []string) []Section {
	type def struct {
		name  string
		start int // 0-based, comment included
	}
	// A type body being read: where its opener sits and how far its members
	// are indented (-1 until the first member line).
	type body struct{ indent, member int }
	var defs []def
	var stack []*body
	for i, l := range lines {
		if strings.TrimSpace(l) == "" || IsCommentOnly(path, l) {
			continue
		}
		ind := indentOf(l)
		for len(stack) > 0 && stack[len(stack)-1].indent >= ind {
			stack = stack[:len(stack)-1]
		}
		inBody := false
		if len(stack) > 0 {
			top := stack[len(stack)-1]
			if top.member < 0 {
				top.member = ind
			}
			inBody = ind == top.member
		}
		if ind > 0 && !inBody {
			continue
		}
		name, value := define(l)
		if name == "" && inBody {
			if m := method.FindStringSubmatch(strings.TrimLeft(l, " \t")); m != nil && !statementWords[m[1]] && m[1] != "catch" {
				name, value = m[1], false
			}
		}
		if name == "" {
			continue
		}
		if value && ind == 0 && arrowFunc.MatchString(l) {
			value = false
		}
		isOpener := typeOpener.MatchString(strings.TrimLeft(l, " \t"))
		// A member declared without a body (a prototype) is not a section.
		prototype := ind > 0 && strings.HasSuffix(strings.TrimSpace(l), ";")
		if !value && !prototype {
			start := i
			for start > 0 && strings.TrimSpace(lines[start-1]) != "" && IsCommentOnly(path, lines[start-1]) {
				start--
			}
			defs = append(defs, def{name, start})
		}
		if isOpener {
			stack = append(stack, &body{ind, -1})
		}
	}
	// end gives the 1-based last line of a range that stops before 0-based
	// line next, without trailing blank lines.
	end := func(next int) int {
		for next > 0 && strings.TrimSpace(lines[next-1]) == "" {
			next--
		}
		return next
	}
	var out []Section
	firstStart := len(lines)
	if len(defs) > 0 {
		firstStart = defs[0].start
	}
	if e := end(firstStart); strings.TrimSpace(strings.Join(lines[:e], "")) != "" {
		out = append(out, Section{"", 1, e})
	}
	for k, d := range defs {
		next := len(lines)
		if k+1 < len(defs) {
			next = defs[k+1].start
		}
		out = append(out, Section{d.name, d.start + 1, end(next)})
	}
	return out
}
