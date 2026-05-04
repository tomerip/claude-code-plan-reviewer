package render

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// Heading is one entry in the document outline.
type Heading struct {
	Level int    // 1..6
	Text  string // plain text
	ID    string // slug used in anchor
}

// Result is the rendered HTML plus the collected outline.
type Result struct {
	HTML     string
	Headings []Heading
}

// Render converts plan markdown into HTML with data-line-start/-end attributes
// on each top-level block, so the frontend can map DOM selections back to
// source line numbers.
//
// This is a minimal, opinionated renderer for the subset of CommonMark that
// actually appears in Claude plan files.
func Render(md string) Result {
	lines := strings.Split(md, "\n")
	r := &renderer{lines: lines}
	r.run()
	return Result{HTML: r.out.String(), Headings: r.headings}
}

// renderInner renders markdown without wrapping each block in a pr-block div.
// Used for recursive renders (e.g. blockquote contents) where wrapping would
// emit nested wrappers whose data-line-start is relative to the inner content,
// confusing the frontend's DOM-to-source line mapping.
func renderInner(md string) string {
	lines := strings.Split(md, "\n")
	r := &renderer{lines: lines, inner: true}
	r.run()
	return r.out.String()
}

type renderer struct {
	lines    []string
	i        int
	out      strings.Builder
	headings []Heading
	inner    bool // when true, emit block HTML directly without pr-block wrapper
}

func (r *renderer) run() {
	for r.i < len(r.lines) {
		line := r.lines[r.i]
		trimmed := strings.TrimLeft(line, " \t")

		switch {
		case strings.TrimSpace(line) == "":
			r.i++
		case isHR(trimmed):
			r.emitBlock(r.i, r.i, "<hr>")
			r.i++
		case isHeading(trimmed):
			r.parseHeading()
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			r.parseFencedCode()
		case strings.HasPrefix(trimmed, "> "):
			r.parseBlockquote()
		case isListItem(trimmed):
			r.parseList()
		case isTableHeader(r.lines, r.i):
			r.parseTable()
		default:
			r.parseParagraph()
		}
	}
}

var hrRe = regexp.MustCompile(`^(-{3,}|\*{3,}|_{3,})\s*$`)

func isHR(s string) bool { return hrRe.MatchString(s) }

var listItemRe = regexp.MustCompile(`^(\s*)([-*+]|\d+\.)\s+`)

func isListItem(s string) bool {
	return listItemRe.MatchString(s)
}

func isTableHeader(lines []string, i int) bool {
	if i+1 >= len(lines) {
		return false
	}
	head := strings.TrimSpace(lines[i])
	sep := strings.TrimSpace(lines[i+1])
	if !strings.Contains(head, "|") || !strings.Contains(sep, "|") {
		return false
	}
	// Separator must be made of dashes/colons/pipes only.
	for _, c := range sep {
		if c != '-' && c != ':' && c != '|' && c != ' ' {
			return false
		}
	}
	return strings.Count(sep, "-") >= 3
}

// isHeading reports whether trimmed starts with 1-6 '#' chars followed by a space.
// This matches CommonMark ATX heading syntax and avoids routing invalid forms
// (e.g. "#foo" with no space) to parseHeading, which would then drop the line.
func isHeading(trimmed string) bool {
	level := 0
	for level < 6 && level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	return level > 0 && level < len(trimmed) && trimmed[level] == ' '
}

func (r *renderer) parseHeading() {
	line := r.lines[r.i]
	trimmed := strings.TrimLeft(line, " \t")
	level := 0
	for level < 6 && level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	// Guaranteed valid by isHeading dispatch, but keep the guard for safety.
	if level == 0 || level >= len(trimmed) || trimmed[level] != ' ' {
		r.parseParagraph()
		return
	}
	text := strings.TrimSpace(trimmed[level+1:])
	text = strings.TrimRight(text, "#")
	text = strings.TrimSpace(text)
	id := slugify(text)
	r.headings = append(r.headings, Heading{Level: level, Text: text, ID: id})

	r.emitBlock(r.i, r.i,
		fmt.Sprintf(`<h%d id="%s">%s</h%d>`, level, html.EscapeString(id), renderInline(text), level))
	r.i++
}

func (r *renderer) parseFencedCode() {
	start := r.i
	openLine := strings.TrimLeft(r.lines[r.i], " \t")
	fence := "```"
	if strings.HasPrefix(openLine, "~~~") {
		fence = "~~~"
	}
	lang := strings.TrimSpace(strings.TrimPrefix(openLine, fence))
	r.i++
	var codeLines []string
	for r.i < len(r.lines) {
		line := r.lines[r.i]
		if strings.HasPrefix(strings.TrimSpace(line), fence) {
			r.i++
			break
		}
		codeLines = append(codeLines, line)
		r.i++
	}
	code := html.EscapeString(strings.Join(codeLines, "\n"))
	langAttr := ""
	if lang != "" {
		langAttr = fmt.Sprintf(` class="language-%s"`, html.EscapeString(lang))
	}
	r.emitBlock(start, r.i-1, fmt.Sprintf("<pre><code%s>%s</code></pre>", langAttr, code))
}

func (r *renderer) parseBlockquote() {
	start := r.i
	var body []string
	for r.i < len(r.lines) {
		line := r.lines[r.i]
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "> ") {
			body = append(body, trimmed[2:])
		} else if trimmed == ">" {
			body = append(body, "")
		} else {
			break
		}
		r.i++
	}
	content := strings.Join(body, "\n")
	inner := renderInner(content)

	// Detect feedback blockquotes and style them distinctly.
	cls := ""
	if strings.HasPrefix(strings.TrimSpace(content), "💬 FEEDBACK:") ||
		strings.HasPrefix(strings.TrimSpace(content), "FEEDBACK:") {
		cls = ` class="pr-feedback"`
	}
	r.emitBlock(start, r.i-1, fmt.Sprintf("<blockquote%s>%s</blockquote>", cls, inner))
}

func (r *renderer) parseList() {
	start := r.i
	var items []listItem
	indentBase := -1

	for r.i < len(r.lines) {
		line := r.lines[r.i]
		if strings.TrimSpace(line) == "" {
			// Peek next non-blank line; if still a list item at same indent, continue.
			j := r.i + 1
			for j < len(r.lines) && strings.TrimSpace(r.lines[j]) == "" {
				j++
			}
			if j < len(r.lines) && isListItem(r.lines[j]) {
				m := listItemRe.FindStringSubmatch(r.lines[j])
				if indentBase < 0 || len(m[1]) == indentBase {
					r.i = j
					continue
				}
			}
			break
		}
		m := listItemRe.FindStringSubmatch(line)
		if m == nil {
			// Continuation of previous item.
			if len(items) == 0 {
				break
			}
			items[len(items)-1].text += "\n" + strings.TrimSpace(line)
			r.i++
			continue
		}
		if indentBase < 0 {
			indentBase = len(m[1])
		}
		if len(m[1]) != indentBase {
			// Nested list; append raw to parent text for now (keep it simple).
			items[len(items)-1].text += "\n" + line
			r.i++
			continue
		}
		marker := m[2]
		text := line[len(m[0]):]
		ordered := !(marker == "-" || marker == "*" || marker == "+")
		items = append(items, listItem{text: text, ordered: ordered})
		r.i++
	}

	if len(items) == 0 {
		return
	}
	tag := "ul"
	if items[0].ordered {
		tag = "ol"
	}
	var b strings.Builder
	b.WriteString("<")
	b.WriteString(tag)
	b.WriteString(">")
	for _, it := range items {
		b.WriteString("<li>")
		b.WriteString(renderInline(it.text))
		b.WriteString("</li>")
	}
	b.WriteString("</")
	b.WriteString(tag)
	b.WriteString(">")
	r.emitBlock(start, r.i-1, b.String())
}

type listItem struct {
	text    string
	ordered bool
}

func (r *renderer) parseTable() {
	start := r.i
	headerCells := splitTableRow(r.lines[r.i])
	r.i += 2 // skip header and separator

	var rows [][]string
	for r.i < len(r.lines) {
		line := r.lines[r.i]
		if strings.TrimSpace(line) == "" || !strings.Contains(line, "|") {
			break
		}
		rows = append(rows, splitTableRow(line))
		r.i++
	}

	var b strings.Builder
	b.WriteString("<table><thead><tr>")
	for _, c := range headerCells {
		b.WriteString("<th>")
		b.WriteString(renderInline(c))
		b.WriteString("</th>")
	}
	b.WriteString("</tr></thead><tbody>")
	for _, row := range rows {
		b.WriteString("<tr>")
		for _, c := range row {
			b.WriteString("<td>")
			b.WriteString(renderInline(c))
			b.WriteString("</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>")
	r.emitBlock(start, r.i-1, b.String())
}

func splitTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func (r *renderer) parseParagraph() {
	start := r.i
	var buf []string
	for r.i < len(r.lines) {
		line := r.lines[r.i]
		trimmed := strings.TrimLeft(line, " \t")
		if strings.TrimSpace(line) == "" ||
			isHeading(trimmed) ||
			strings.HasPrefix(trimmed, "```") ||
			strings.HasPrefix(trimmed, "~~~") ||
			strings.HasPrefix(trimmed, "> ") ||
			isListItem(trimmed) ||
			isHR(trimmed) ||
			isTableHeader(r.lines, r.i) {
			break
		}
		buf = append(buf, line)
		r.i++
	}
	if len(buf) == 0 {
		r.i++
		return
	}
	text := strings.Join(buf, " ")
	r.emitBlock(start, r.i-1, "<p>"+renderInline(text)+"</p>")
}

func (r *renderer) emitBlock(start, end int, html string) {
	if r.inner {
		r.out.WriteString(html)
		r.out.WriteByte('\n')
		return
	}
	fmt.Fprintf(&r.out, `<div class="pr-block" data-line-start="%d" data-line-end="%d">%s</div>`+"\n",
		start+1, end+1, html)
}

// --- inline rendering -------------------------------------------------------

var (
	codeSpanRe = regexp.MustCompile("`([^`]+)`")
	boldRe     = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	italicRe   = regexp.MustCompile(`\*([^*\n]+)\*`)
	linkRe     = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

func renderInline(s string) string {
	// Order matters. Protect code spans first (escape their content, don't further process).
	type span struct {
		start, end int
		html       string
	}
	var spans []span

	for _, m := range codeSpanRe.FindAllStringSubmatchIndex(s, -1) {
		content := s[m[2]:m[3]]
		spans = append(spans, span{m[0], m[1], "<code>" + html.EscapeString(content) + "</code>"})
	}

	if len(spans) == 0 {
		return processNonCode(s)
	}

	// Stitch together, processing non-code regions.
	var b strings.Builder
	prev := 0
	for _, sp := range spans {
		if sp.start > prev {
			b.WriteString(processNonCode(s[prev:sp.start]))
		}
		b.WriteString(sp.html)
		prev = sp.end
	}
	if prev < len(s) {
		b.WriteString(processNonCode(s[prev:]))
	}
	return b.String()
}

func processNonCode(s string) string {
	// Escape HTML first.
	escaped := html.EscapeString(s)
	// Links: [text](url) — replace BEFORE bold/italic so they don't mangle.
	escaped = linkRe.ReplaceAllStringFunc(escaped, func(m string) string {
		parts := linkRe.FindStringSubmatch(m)
		u := strings.TrimSpace(parts[2])
		if !isSafeURL(u) {
			// Render link text as plain text; drop the URL entirely.
			return parts[1]
		}
		return fmt.Sprintf(`<a href="%s" target="_blank" rel="noopener noreferrer">%s</a>`,
			html.EscapeString(u), parts[1])
	})
	// Bold first (longer delimiter).
	escaped = boldRe.ReplaceAllString(escaped, "<strong>$1</strong>")
	// Italic: single *.
	escaped = italicRe.ReplaceAllString(escaped, "<em>$1</em>")
	return escaped
}

// isSafeURL returns true only for schemes/paths that can't execute scripts.
// Rejects javascript:, data:, vbscript:, file:, and any other unexpected scheme.
func isSafeURL(u string) bool {
	if u == "" {
		return false
	}
	low := strings.ToLower(u)
	switch {
	case strings.HasPrefix(low, "http://"),
		strings.HasPrefix(low, "https://"),
		strings.HasPrefix(low, "mailto:"),
		strings.HasPrefix(low, "#"),
		strings.HasPrefix(low, "/"),
		strings.HasPrefix(low, "./"),
		strings.HasPrefix(low, "../"):
		return true
	}
	// If there's no scheme separator, treat as relative (safe).
	if !strings.Contains(low, ":") {
		return true
	}
	return false
}

// --- slugify ----------------------------------------------------------------

var slugBadChars = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = slugBadChars.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "section"
	}
	return s
}
