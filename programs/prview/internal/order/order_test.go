package order

import "testing"

func TestFromDescription(t *testing.T) {
	body := "Adds retries.\n\n## Suggested reading order\n\n" +
		"1. `api/handler.go:CreateJob` — where it starts: new retry flag\n" +
		"2. `jobs/queue.go:Enqueue`: retry count passed through here,\n" +
		"   and stored per item\n" +
		"3. jobs/retry.go:backoff - new helper, check the cap\n" +
		"4. `jobs/retry_test.go` — tests for the backoff\n" +
		"5. `src/camera_chain.cpp:CameraChain::Build` — C++ method\n\n" +
		"Renames of Job to Task are mechanical.\n\n## Testing\n1. not a step.go:x\n"
	o := FromDescription(body)
	if o == nil {
		t.Fatal("no order")
	}
	want := []Step{
		{File: "api/handler.go", Function: "CreateJob", Note: "where it starts: new retry flag"},
		{File: "jobs/queue.go", Function: "Enqueue", Note: "retry count passed through here, and stored per item"},
		{File: "jobs/retry.go", Function: "backoff", Note: "new helper, check the cap"},
		{File: "jobs/retry_test.go", Function: "", Note: "tests for the backoff"},
		{File: "src/camera_chain.cpp", Function: "CameraChain::Build", Note: "C++ method"},
	}
	if len(o.Steps) != len(want) {
		t.Fatalf("got %d steps: %+v", len(o.Steps), o.Steps)
	}
	for i, w := range want {
		g := o.Steps[i]
		if g.File != w.File || g.Function != w.Function || g.Note != w.Note {
			t.Errorf("step %d: got %+v want %+v", i, g, w)
		}
	}
}

func TestFromDescriptionNone(t *testing.T) {
	if o := FromDescription("Just a fix.\n\n1. first\n2. second"); o != nil {
		t.Fatalf("expected none, got %+v", o)
	}
	if o := FromDescription("**Suggested reading order**\n\nnothing listed"); o != nil {
		t.Fatalf("expected none, got %+v", o)
	}
}

func TestLocate(t *testing.T) {
	goFile := []string{
		"package jobs",
		"// Enqueue adds a job",
		"func (q *Queue) Enqueue(ctx context.Context, j Job) error {",
		"	return q.Enqueue2(j)",
		"}",
	}
	if got := Locate(goFile, "Enqueue"); got != 3 {
		t.Errorf("go: got %d", got)
	}
	cpp := []string{
		"#include \"x.h\"",
		"static int helper();",
		"Chain CameraChain::Build(const Cfg& c)",
		"{",
		"  return helper();",
		"}",
	}
	if got := Locate(cpp, "CameraChain::Build"); got != 3 {
		t.Errorf("cpp: got %d", got)
	}
	if got := Locate(cpp, "helper"); got != 2 {
		t.Errorf("cpp helper: got %d", got)
	}
	if got := Locate(goFile, "Missing"); got != 0 {
		t.Errorf("missing: got %d", got)
	}
}
