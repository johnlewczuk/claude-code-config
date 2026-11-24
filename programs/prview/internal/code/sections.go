package code

import (
	"regexp"
	"strings"
)

// DefinedName returns the name the line defines and the 1-based byte column
// where that name starts, or "" and 0 when the line defines nothing. See
// IsDefinition for what counts.
func DefinedName(line string) (name string, col int) {
	d := define(line)
	return d.name, d.at + 1
}

// definition is what a line defines.
type definition struct {
	name  string
	at    int  // where name starts in the line, in bytes from 0; -1 with no name
	value bool // a plain value (a constant, variable or field), not a function or type
}

var noDefinition = definition{at: -1}

func define(line string) definition {
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
	// in turns a place in rest into a place in line.
	in := func(d definition) definition {
		if d.name != "" {
			d.at += len(line) - len(rest)
		}
		return d
	}
	skip(modifiers)
	w := skip(defWord)
	if w == nil {
		d, isCall := declaredName(rest, indented, skipped, line)
		d.value = !isCall
		return in(d)
	}
	skip(receiver)
	skip(typeParams)
	skip(modifiers)
	// func Run, class Foo, let x, #define MAX: the first name. After a word
	// that can also begin a C type, the declarator is tried first:
	// "const std::string& name()" defines name, not string.
	isValueWord := w[1] == "let" || w[1] == "var" || w[1] == "val" || w[1] == "const"
	if cTypeWords[w[1]] {
		if d, isCall := declaredName(rest, indented, skipped, line); d.name != "" {
			d.value = w[1] == "const" && !isCall
			return in(d)
		}
	}
	if m := firstName.FindStringSubmatchIndex(rest); m != nil {
		return in(definition{name: rest[m[2]:m[3]], at: m[2], value: isValueWord})
	}
	return noDefinition
}

// declaredName reads a C and C++ style declaration: a type, then the name.
// The definition's place is in rest. isCall says the name is followed by "(".
func declaredName(rest string, indented, skipped bool, line string) (d definition, isCall bool) {
	// A type, then the name: int helper(), void Node::Run(), public
	// getUser(), static const int kMax = 5.
	m := declarator.FindStringSubmatchIndex(rest)
	if m == nil {
		return noDefinition, false
	}
	typ, follow := "", rest[m[6]:m[7]]
	if m[2] >= 0 {
		typ = rest[m[2]:m[3]]
	}
	for _, w := range words.FindAllString(typ, -1) {
		if statementWords[w] {
			return noDefinition, false
		}
	}
	found := definition{name: rest[m[4]:m[5]], at: m[4]}
	isCall = follow == "("
	switch {
	case skipped:
		return found, isCall
	case !isCall:
		// "x, y = f()" and "err := f()" assign; only a modifier or a
		// definition word makes these a declaration.
		return noDefinition, false
	case typ != "":
		return found, true
	}
	// No type: a constructor (Foo::Foo(...)) or a function whose return type
	// is on the line above, at the start of a line not ending in ";".
	if !indented && !strings.HasSuffix(strings.TrimSpace(line), ";") {
		return found, true
	}
	return noDefinition, false
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

// preamble matches a line that brings in other code or says which package
// the file is in. Such lines belong to the top of the file.
var preamble = regexp.MustCompile(`^(?:import|package|#\s*include|from\s+\S+\s+import|using|require)\b`)

// namespace matches a namespace or module opener. It holds the file's
// sections rather than being one, so it belongs to the section above it.
var namespace = regexp.MustCompile(`^(?:(?:export|declare)\s+)*(?:namespace|module)\b`)

// decorator matches a decorator or annotation line, as in "@property" or
// "@Override". It belongs to the definition below it, like a comment.
var decorator = regexp.MustCompile(`^@\w`)

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// Sections splits a file into the top of the file (Name "", when it holds
// anything but blank lines) and one section per function or type. A section
// starts at the comment and decorators directly above its definition and
// runs to the line before the next section, minus trailing blank lines.
// Definitions at the left edge always start a section; indented ones only
// when they are direct members of a type (class, struct, interface, impl,
// trait or enum) or a namespace, so locals inside a function don't.
// Constants, variables and fields never do, except those holding a function.
// Imports, package lines and namespace openers never do either.
func Sections(path string, lines []string) []Section {
	type def struct {
		name  string
		start int // 0-based, comment included
	}
	// A type or namespace body being read: where its opener sits and how far its members
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
		trimmed := strings.TrimLeft(l, " \t")
		if (ind > 0 && !inBody) || preamble.MatchString(trimmed) {
			continue
		}
		d := define(l)
		name, value := d.name, d.value
		if name == "" && inBody {
			if m := method.FindStringSubmatch(trimmed); m != nil && !statementWords[m[1]] && m[1] != "catch" {
				name, value = m[1], false
			}
		}
		if name == "" {
			continue
		}
		if value && ind == 0 && arrowFunc.MatchString(l) {
			value = false
		}
		isOpener := typeOpener.MatchString(trimmed)
		// A member declared without a body (a prototype) is not a section.
		prototype := ind > 0 && strings.HasSuffix(strings.TrimSpace(l), ";")
		if !value && !prototype && !namespace.MatchString(trimmed) {
			start := i
			for start > 0 && strings.TrimSpace(lines[start-1]) != "" &&
				(IsCommentOnly(path, lines[start-1]) || decorator.MatchString(strings.TrimLeft(lines[start-1], " \t"))) {
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
