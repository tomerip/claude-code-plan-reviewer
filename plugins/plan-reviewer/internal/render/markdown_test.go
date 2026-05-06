package render

import (
	"strings"
	"testing"
)

func TestRender_Headings(t *testing.T) {
	r := Render("# H1\n\n## H2\n\n### H3\n\n#### H4")
	wants := []string{
		`<h1 id="h1">H1</h1>`,
		`<h2 id="h2">H2</h2>`,
		`<h3 id="h3">H3</h3>`,
		`<h4 id="h4">H4</h4>`,
	}
	for _, w := range wants {
		if !strings.Contains(r.HTML, w) {
			t.Errorf("missing %q in %s", w, r.HTML)
		}
	}
	if len(r.Headings) != 4 {
		t.Fatalf("want 4 headings, got %d", len(r.Headings))
	}
	if r.Headings[0].Level != 1 || r.Headings[0].Text != "H1" || r.Headings[0].ID != "h1" {
		t.Errorf("bad first heading: %+v", r.Headings[0])
	}
}

func TestRender_Paragraph(t *testing.T) {
	r := Render("hello world\nsecond line")
	if !strings.Contains(r.HTML, "<p>hello world second line</p>") {
		t.Errorf("unexpected paragraph rendering: %s", r.HTML)
	}
}

func TestRender_CodeFence(t *testing.T) {
	md := "```go\nfunc main() {}\n```"
	r := Render(md)
	if !strings.Contains(r.HTML, `<pre><code class="language-go">func main() {}</code></pre>`) {
		t.Errorf("unexpected code fence: %s", r.HTML)
	}
}

func TestRender_Blockquote(t *testing.T) {
	r := Render("> hello\n> world")
	if !strings.Contains(r.HTML, "<blockquote>") || !strings.Contains(r.HTML, "hello world") {
		t.Errorf("unexpected blockquote: %s", r.HTML)
	}
}

func TestRender_FeedbackBlockquote(t *testing.T) {
	r := Render("> 💬 FEEDBACK: test")
	if !strings.Contains(r.HTML, `<blockquote class="pr-feedback">`) {
		t.Errorf("feedback blockquote should get pr-feedback class: %s", r.HTML)
	}
}

func TestRender_FeedbackBlockquote_WithAnchorSnippet(t *testing.T) {
	// Headers may include the highlighted anchor in quotes —
	// `FEEDBACK on "..."` — and still need the pr-feedback class.
	r := Render(`> 💬 FEEDBACK on "some span": test`)
	if !strings.Contains(r.HTML, `<blockquote class="pr-feedback">`) {
		t.Errorf("feedback blockquote with anchor snippet should get pr-feedback class: %s", r.HTML)
	}
}

func TestRender_UnorderedList(t *testing.T) {
	r := Render("- one\n- two\n- three")
	if !strings.Contains(r.HTML, "<ul>") || !strings.Contains(r.HTML, "<li>one</li>") {
		t.Errorf("unexpected ul: %s", r.HTML)
	}
}

func TestRender_OrderedList(t *testing.T) {
	r := Render("1. one\n2. two")
	if !strings.Contains(r.HTML, "<ol>") || !strings.Contains(r.HTML, "<li>one</li>") {
		t.Errorf("unexpected ol: %s", r.HTML)
	}
}

func TestRender_Table(t *testing.T) {
	md := "| a | b |\n|---|---|\n| 1 | 2 |"
	r := Render(md)
	for _, s := range []string{"<table>", "<th>a</th>", "<th>b</th>", "<td>1</td>", "<td>2</td>"} {
		if !strings.Contains(r.HTML, s) {
			t.Errorf("table missing %q: %s", s, r.HTML)
		}
	}
}

func TestRender_HorizontalRule(t *testing.T) {
	r := Render("para\n\n---\n\nmore")
	if !strings.Contains(r.HTML, "<hr>") {
		t.Errorf("missing hr: %s", r.HTML)
	}
}

func TestRender_LineNumbers(t *testing.T) {
	md := "# Title\n\nPara line.\n\n- item"
	r := Render(md)
	// Title is line 1, paragraph is line 3, list starts at line 5.
	if !strings.Contains(r.HTML, `data-line-start="1"`) {
		t.Errorf("expected data-line-start=1 for title: %s", r.HTML)
	}
	if !strings.Contains(r.HTML, `data-line-start="3"`) {
		t.Errorf("expected data-line-start=3 for para: %s", r.HTML)
	}
	if !strings.Contains(r.HTML, `data-line-start="5"`) {
		t.Errorf("expected data-line-start=5 for list: %s", r.HTML)
	}
}

func TestRender_Blockquote_NoNestedBlocks(t *testing.T) {
	// The inner content of a blockquote should NOT emit pr-block wrappers
	// (those have line numbers relative to the inner content, which breaks
	// frontend line-mapping).
	r := Render("> hello\n> world")
	outerCount := strings.Count(r.HTML, `class="pr-block"`)
	if outerCount != 1 {
		t.Errorf("expected exactly 1 pr-block for blockquote, got %d: %s", outerCount, r.HTML)
	}
}

func TestRender_Inline_Bold(t *testing.T) {
	r := Render("**hello**")
	if !strings.Contains(r.HTML, "<strong>hello</strong>") {
		t.Errorf("bold: %s", r.HTML)
	}
}

func TestRender_Inline_Italic(t *testing.T) {
	r := Render("*hello*")
	if !strings.Contains(r.HTML, "<em>hello</em>") {
		t.Errorf("italic: %s", r.HTML)
	}
}

func TestRender_Inline_Code(t *testing.T) {
	r := Render("use `fmt.Println` to print")
	if !strings.Contains(r.HTML, "<code>fmt.Println</code>") {
		t.Errorf("inline code: %s", r.HTML)
	}
}

func TestRender_Inline_CodeEscapesHTML(t *testing.T) {
	r := Render("`<script>alert(1)</script>`")
	if !strings.Contains(r.HTML, "&lt;script&gt;") {
		t.Errorf("code must escape HTML: %s", r.HTML)
	}
	if strings.Contains(r.HTML, "<script>alert(1)</script>") {
		t.Errorf("script tag leaked through code: %s", r.HTML)
	}
}

func TestRender_XSS_HeadingEscape(t *testing.T) {
	r := Render("# <script>alert(1)</script>")
	if strings.Contains(r.HTML, "<script>alert(1)</script>") {
		t.Errorf("script tag must be escaped in heading: %s", r.HTML)
	}
	if !strings.Contains(r.HTML, "&lt;script&gt;") {
		t.Errorf("expected escaped script in heading: %s", r.HTML)
	}
}

func TestRender_XSS_ParagraphEscape(t *testing.T) {
	r := Render("hello <img src=x onerror=alert(1)>")
	if strings.Contains(r.HTML, "<img src=x") {
		t.Errorf("img tag must be escaped: %s", r.HTML)
	}
}

func TestRender_XSS_TableCellEscape(t *testing.T) {
	md := "| a | b |\n|---|---|\n| <script>x</script> | safe |"
	r := Render(md)
	if strings.Contains(r.HTML, "<script>x</script>") {
		t.Errorf("script in table cell must be escaped: %s", r.HTML)
	}
}

func TestRender_LinkSchemes_Allow(t *testing.T) {
	cases := []struct {
		md   string
		href string
	}{
		{"[x](http://example.com)", "http://example.com"},
		{"[x](https://example.com)", "https://example.com"},
		{"[x](mailto:me@example.com)", "mailto:me@example.com"},
		{"[x](#section)", "#section"},
		{"[x](/abs)", "/abs"},
		{"[x](./rel)", "./rel"},
		{"[x](../up)", "../up"},
	}
	for _, c := range cases {
		t.Run(c.href, func(t *testing.T) {
			r := Render(c.md)
			if !strings.Contains(r.HTML, `href="`+c.href+`"`) {
				t.Errorf("expected href=%q in: %s", c.href, r.HTML)
			}
			if !strings.Contains(r.HTML, `rel="noopener noreferrer"`) {
				t.Errorf("expected noreferrer: %s", r.HTML)
			}
		})
	}
}

func TestRender_LinkSchemes_Block(t *testing.T) {
	cases := []string{
		"[click](javascript:alert(1))",
		"[click](JAVASCRIPT:alert(1))",
		"[click](  javascript:alert(1))",
		"[click](data:text/html,<script>alert(1)</script>)",
		"[click](vbscript:msgbox)",
		"[click](file:///etc/passwd)",
	}
	for _, md := range cases {
		t.Run(md, func(t *testing.T) {
			r := Render(md)
			if strings.Contains(r.HTML, "<a href") {
				t.Errorf("dangerous scheme should not render as link: %s", r.HTML)
			}
			if !strings.Contains(r.HTML, "click") {
				t.Errorf("link text should still appear as plain text: %s", r.HTML)
			}
		})
	}
}

func TestRender_MalformedHeading_NotDropped(t *testing.T) {
	// "#foo" (no space after #) is not a valid ATX heading. It should render
	// as a paragraph, not be silently dropped.
	r := Render("#foo")
	if !strings.Contains(r.HTML, "#foo") {
		t.Errorf("malformed heading content lost: %s", r.HTML)
	}
	if strings.Contains(r.HTML, "<h1") {
		t.Errorf("malformed heading should not produce h1: %s", r.HTML)
	}
}

func TestRender_SevenHashes_NotHeading(t *testing.T) {
	// 7+ hashes is invalid; should render as paragraph, not drop.
	r := Render("####### too many")
	if !strings.Contains(r.HTML, "too many") {
		t.Errorf("content lost: %s", r.HTML)
	}
}

func TestRender_EmptyInput(t *testing.T) {
	r := Render("")
	if r.HTML != "" {
		t.Errorf("expected empty, got %s", r.HTML)
	}
}

func TestRender_Slugify(t *testing.T) {
	cases := map[string]string{
		"Hello World":                "hello-world",
		"foo/bar/baz":                "foo-bar-baz",
		"  spaces  ":                 "spaces",
		"!!! punct ???":              "punct",
		"Über Café":                  "ber-caf", // non-ASCII stripped
	}
	for in, want := range cases {
		got := slugify(in)
		if got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsSafeURL(t *testing.T) {
	safe := []string{"http://a", "https://a", "mailto:x", "#a", "/a", "./a", "../a", "relative"}
	unsafe := []string{"", "javascript:x", "JAVASCRIPT:x", "data:x", "vbscript:x", "file:x", "gopher:x"}
	for _, u := range safe {
		if !isSafeURL(u) {
			t.Errorf("%q should be safe", u)
		}
	}
	for _, u := range unsafe {
		if isSafeURL(u) {
			t.Errorf("%q should be unsafe", u)
		}
	}
}
