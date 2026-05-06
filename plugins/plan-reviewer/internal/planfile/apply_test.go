package planfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempPlan(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestApplyFeedback_SingleAnchor(t *testing.T) {
	md := "# Title\n\nfirst paragraph.\n\nsecond paragraph.\n"
	path := writeTempPlan(t, md)

	got, err := ApplyFeedback(path, []Comment{
		{AnchorText: "first", LineStart: 3, LineEnd: 3, Body: "why?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "> 💬 FEEDBACK: why?") {
		t.Errorf("feedback not inserted: %s", got)
	}
	// Feedback should come after the first paragraph, before the second.
	fbIdx := strings.Index(got, "FEEDBACK:")
	secondIdx := strings.Index(got, "second paragraph")
	if fbIdx == -1 || secondIdx == -1 || fbIdx > secondIdx {
		t.Errorf("feedback in wrong position:\n%s", got)
	}
}

func TestApplyFeedback_MultipleAnchors_ReverseOrder(t *testing.T) {
	md := "alpha\n\nbeta\n\ngamma\n\ndelta\n"
	path := writeTempPlan(t, md)

	// Comments on lines 1, 3, 5 — should be inserted in reverse so earlier
	// inserts don't shift later anchors.
	got, err := ApplyFeedback(path, []Comment{
		{AnchorText: "alpha", LineStart: 1, Body: "A"},
		{AnchorText: "beta", LineStart: 3, Body: "B"},
		{AnchorText: "gamma", LineStart: 5, Body: "C"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FEEDBACK: A", "FEEDBACK: B", "FEEDBACK: C"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
	// Original content must still be intact.
	for _, w := range []string{"alpha", "beta", "gamma", "delta"} {
		if !strings.Contains(got, w) {
			t.Errorf("original content %q lost: %s", w, got)
		}
	}
}

func TestApplyFeedback_AnchorNotFound_AppendsEOF(t *testing.T) {
	md := "only line\n"
	path := writeTempPlan(t, md)

	got, err := ApplyFeedback(path, []Comment{
		{AnchorText: "NOTEXIST", LineStart: 99, Body: "orphan"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "FEEDBACK: orphan") {
		t.Errorf("orphan comment not appended: %s", got)
	}
	if strings.Index(got, "only line") > strings.Index(got, "FEEDBACK") {
		t.Errorf("orphan should be after original content: %s", got)
	}
}

func TestApplyFeedback_StackedFeedbacks_SingleBlankLine(t *testing.T) {
	md := "line one\nline two\n"
	path := writeTempPlan(t, md)
	got, err := ApplyFeedback(path, []Comment{
		{AnchorText: "line one", LineStart: 1, Body: "first"},
		{AnchorText: "line two", LineStart: 2, Body: "second"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// There should never be two consecutive blank lines between feedbacks.
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("unexpected double blank line:\n%s", got)
	}
}

func TestApplyFeedback_DoesNotWriteToDisk(t *testing.T) {
	// ExitPlanMode.call() writes updatedInput.plan to the plan file itself.
	// If ApplyFeedback ALSO writes, the file's mtime gets bumped twice,
	// past Claude's cached readFileState timestamp — the subsequent Write
	// during revision fails with "File has been modified since read".
	md := "one\n\ntwo\n"
	path := writeTempPlan(t, md)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyFeedback(path, []Comment{{AnchorText: "one", LineStart: 1, Body: "x"}}); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("plan file mtime changed; ApplyFeedback must be read-only")
	}
	// And the file contents must be unchanged.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != md {
		t.Errorf("plan file content changed; ApplyFeedback must be read-only.\nwant: %q\ngot:  %q", md, string(raw))
	}
}

func TestFindAnchor_NearestToHint(t *testing.T) {
	lines := []string{"foo", "bar", "foo", "baz", "foo"}
	c := Comment{AnchorText: "foo", LineStart: 3}
	got := findAnchor(lines, c)
	if got != 2 { // 0-indexed line 2 = "foo" at hint 3
		t.Errorf("want nearest-to-3 foo = index 2, got %d", got)
	}
}

func TestFindAnchor_Empty(t *testing.T) {
	if findAnchor([]string{"x"}, Comment{AnchorText: ""}) != -1 {
		t.Error("empty anchor should return -1")
	}
}
