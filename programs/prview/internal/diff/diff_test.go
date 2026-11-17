package diff

import (
	"encoding/json"
	"strings"
	"testing"
)

const sample = `diff --git a/jobs/queue.go b/jobs/queue.go
index 111..222 100644
--- a/jobs/queue.go
+++ b/jobs/queue.go
@@ -41,4 +41,5 @@ func (q *Queue) Enqueue(ctx context.Context, j Job) error {
 	q.mu.Lock()
-	q.items = append(q.items, item{job: j})
+	q.items = append(q.items, item{job: j, attempt: 0})
+	if j.Retry > 0 {

 	return nil
diff --git a/old name.txt b/new name.txt
similarity index 90%
rename from old name.txt
rename to new name.txt
index 333..444 100644
--- a/old name.txt
+++ b/new name.txt
@@ -1 +1 @@
-hello
+hello world
\ No newline at end of file
diff --git a/img.png b/img.png
new file mode 100644
index 0000000..555
Binary files /dev/null and b/img.png differ
`

func TestParse(t *testing.T) {
	files, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("got %d files", len(files))
	}
	q := files[0]
	if q.Path != "jobs/queue.go" || q.Additions != 2 || q.Deletions != 1 || len(q.Hunks) != 1 {
		t.Fatalf("queue.go: %+v", q)
	}
	h := q.Hunks[0]
	if h.Header != "func (q *Queue) Enqueue(ctx context.Context, j Job) error {" {
		t.Fatalf("header %q", h.Header)
	}
	want := []struct {
		kind     Kind
		old, new int
	}{{Context, 41, 41}, {Removed, 42, 0}, {Added, 0, 42}, {Added, 0, 43}, {Context, 43, 44}, {Context, 44, 45}}
	if len(h.Lines) != len(want) {
		t.Fatalf("lines %d", len(h.Lines))
	}
	for i, w := range want {
		l := h.Lines[i]
		if l.Kind != w.kind || l.Old != w.old || l.New != w.new {
			t.Errorf("line %d: got %s %d %d want %s %d %d", i, l.Kind, l.Old, l.New, w.kind, w.old, w.new)
		}
	}
	r := files[1]
	if r.Status != "renamed" || r.OldPath != "old name.txt" || r.Path != "new name.txt" {
		t.Fatalf("rename: %+v", r)
	}
	b := files[2]
	if !b.Binary || b.Status != "added" || b.Path != "img.png" {
		t.Fatalf("binary: %+v", b)
	}
	js, _ := json.Marshal(h.Lines[1])
	if !strings.HasPrefix(string(js), `{"kind":"del","old":42`) {
		t.Fatalf("json %s", js)
	}
}
