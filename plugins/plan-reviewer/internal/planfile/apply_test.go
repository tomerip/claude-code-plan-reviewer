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

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyFeedback_SingleAnchor(t *testing.T) {
	md := "# Title\n\nfirst paragraph.\n\nsecond paragraph.\n"
	path := writeTempPlan(t, md)

	err := ApplyFeedback(path, []Comment{
		{AnchorText: "first", LineStart: 3, LineEnd: 3, Body: "why?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
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
	err := ApplyFeedback(path, []Comment{
		{AnchorText: "alpha", LineStart: 1, Body: "A"},
		{AnchorText: "beta", LineStart: 3, Body: "B"},
		{AnchorText: "gamma", LineStart: 5, Body: "C"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
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

	err := ApplyFeedback(path, []Comment{
		{AnchorText: "NOTEXIST", LineStart: 99, Body: "orphan"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
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
	err := ApplyFeedback(path, []Comment{
		{AnchorText: "line one", LineStart: 1, Body: "first"},
		{AnchorText: "line two", LineStart: 2, Body: "second"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	// There should never be two consecutive blank lines between feedbacks.
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("unexpected double blank line:\n%s", got)
	}
}

func TestApplyFeedback_RejectsSymlinkWrite(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.md")
	if err := os.WriteFile(real, []byte("real content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks not supported in test env")
	}

	err := ApplyFeedback(link, []Comment{{AnchorText: "real", LineStart: 1, Body: "x"}})
	if err == nil {
		t.Fatalf("expected error writing through symlink, got nil")
	}
	// Real file must be untouched.
	if got := readFile(t, real); got != "real content\n" {
		t.Errorf("real file modified through symlink: %q", got)
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
