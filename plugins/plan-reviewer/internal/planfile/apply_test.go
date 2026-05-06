package planfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
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
	if !strings.Contains(got, `> 💬 FEEDBACK on "first": why?`) {
		t.Errorf("feedback not inserted with anchor snippet: %s", got)
	}
	// Feedback should come after the first paragraph, before the second.
	fbIdx := strings.Index(got, "FEEDBACK")
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
	for _, want := range []string{
		`FEEDBACK on "alpha": A`,
		`FEEDBACK on "beta": B`,
		`FEEDBACK on "gamma": C`,
	} {
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
	// Anchor text is still included in the header even when we couldn't
	// locate it in the plan — Claude should still see what the user
	// highlighted in their browser.
	if !strings.Contains(got, `FEEDBACK on "NOTEXIST": orphan`) {
		t.Errorf("orphan comment not appended with anchor: %s", got)
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

func TestApplyFeedback_AnchorInHeader_MultilineCollapsed(t *testing.T) {
	md := "para one\n\npara two\n"
	path := writeTempPlan(t, md)
	got, err := ApplyFeedback(path, []Comment{
		{AnchorText: "multi\n  line  \n\n\tanchor", LineStart: 1, Body: "hm"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Internal whitespace (newlines, tabs, double spaces) must collapse to
	// single spaces so the blockquote header stays on one line.
	if !strings.Contains(got, `FEEDBACK on "multi line anchor": hm`) {
		t.Errorf("anchor whitespace not normalized:\n%s", got)
	}
}

func TestApplyFeedback_AnchorInHeader_CJKTruncation(t *testing.T) {
	// Truncation is by rune, not byte. A CJK anchor where the 160-rune
	// boundary lands between multi-byte code points must not produce an
	// invalid UTF-8 sequence.
	md := "placeholder\n"
	path := writeTempPlan(t, md)
	anchor := strings.Repeat("日本語", 100) // 300 runes, 900 bytes
	got, err := ApplyFeedback(path, []Comment{
		{AnchorText: anchor, LineStart: 1, Body: "y"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("CJK anchor should truncate with ellipsis:\n%s", got)
	}
	// Output must be valid UTF-8.
	if !utf8.ValidString(got) {
		t.Errorf("truncated CJK output is not valid UTF-8")
	}
}

func TestApplyFeedback_AnchorInHeader_LongTruncated(t *testing.T) {
	md := "whatever\n"
	path := writeTempPlan(t, md)
	long := strings.Repeat("a", 300)
	got, err := ApplyFeedback(path, []Comment{
		{AnchorText: long, LineStart: 1, Body: "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("long anchor should be truncated with ellipsis:\n%s", got)
	}
	// The full 300-char anchor must not appear verbatim.
	if strings.Contains(got, long) {
		t.Errorf("untruncated anchor leaked into output:\n%s", got)
	}
}

func TestApplyFeedback_AnchorInHeader_QuotesEscaped(t *testing.T) {
	md := "whatever\n"
	path := writeTempPlan(t, md)
	got, err := ApplyFeedback(path, []Comment{
		{AnchorText: `the "important" thing`, LineStart: 1, Body: "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Inner double quotes must be swapped for single so they don't terminate
	// the snippet's outer quoting early.
	if !strings.Contains(got, `FEEDBACK on "the 'important' thing": x`) {
		t.Errorf("inner double quotes not swapped:\n%s", got)
	}
}

func TestApplyFeedback_EmptyAnchor_NoSnippet(t *testing.T) {
	md := "whatever\n"
	path := writeTempPlan(t, md)
	got, err := ApplyFeedback(path, []Comment{
		{AnchorText: "", LineStart: 1, Body: "standalone"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// No anchor → header is just "FEEDBACK: <body>", no `on "..."` segment.
	if !strings.Contains(got, "FEEDBACK: standalone") {
		t.Errorf("empty-anchor header missing:\n%s", got)
	}
	if strings.Contains(got, "FEEDBACK on") {
		t.Errorf("empty anchor should not produce `on \"...\"` segment:\n%s", got)
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
