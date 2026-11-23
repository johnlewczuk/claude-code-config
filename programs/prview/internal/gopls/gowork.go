package gopls

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// WriteGoWork writes a go.work file to workFile that loads every Go module
// under checkout together, so a name can be followed from one module into
// another. It returns whether gopls must be pointed at it (with GOWORK).
// It writes nothing and returns false when the checkout has its own go.work
// or has no go.mod.
func WriteGoWork(checkout, workFile string) (useEnv bool, err error) {
	root, err := filepath.Abs(checkout)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(filepath.Join(root, "go.work")); err == nil {
		return false, nil
	}
	var mods []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != root && (name == "vendor" || name == "testdata" || name == "node_modules" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "go.mod" {
			mods = append(mods, filepath.Dir(p))
		}
		return nil
	})
	if err != nil || len(mods) == 0 {
		return false, err
	}
	sort.Strings(mods)
	var b strings.Builder
	b.WriteString("go 1.26\n\nuse (\n")
	for _, m := range mods {
		if strings.ContainsAny(m, " \t\"'`") {
			m = strconv.Quote(m)
		}
		b.WriteString("\t" + m + "\n")
	}
	b.WriteString(")\n")
	if err := os.MkdirAll(filepath.Dir(workFile), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(workFile, []byte(b.String()), 0o644)
}
