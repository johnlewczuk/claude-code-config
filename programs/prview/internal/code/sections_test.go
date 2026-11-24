package code

import (
	"reflect"
	"testing"
)

func TestSections(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		lines []string
		want  []Section
	}{
		{"go", "a.go", []string{
			"package x", // 1
			"",
			`import "fmt"`, // 3
			"const k = 1",  // 4
			"",
			"// A does it.", // 6
			"func A() {",    // 7
			"\tx := f()",
			"\tconst y = 2",
			"}",
			"",
			"func (s *S) B() {}", // 12
			"",
			"type S struct {", // 14
			"}",
		}, []Section{{"", 1, 4}, {"A", 6, 10}, {"B", 12, 12}, {"S", 14, 15}}},
		{"cpp", "a.cc", []string{
			"class Foo {", // 1
			"  void run();",
			"  int size() const;",
			"};",
			"",
			"int Foo::size() const {", // 6
			"  return helper();",
			"}",
		}, []Section{{"Foo", 1, 4}, {"size", 6, 8}}},
		{"ts", "a.ts", []string{
			"export function f() {", // 1
			"  const y = g()",
			"}",
			"class C {", // 4
			"  load(): void {",
			"  }",
			"  private x = 1",
			"}",
		}, []Section{{"f", 1, 3}, {"C", 4, 4}, {"load", 5, 8}}},
		{"python", "a.py", []string{
			"def f():", // 1
			"    pass",
			"class C:", // 3
			"    def m(self):",
			"        x = 1",
		}, []Section{{"f", 1, 2}, {"C", 3, 3}, {"m", 4, 5}}},
		{"none", "a.txt", []string{"", "hello", "world", ""}, []Section{{"", 1, 3}}},
		{"empty", "a.go", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Sections(tt.path, tt.lines); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
