package planfile

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

// maxAnchorLen caps the anchor snippet in the FEEDBACK header. Long enough
// to disambiguate which span the user highlighted; short enough to keep the
// blockquote readable when someone highlights a whole paragraph.
const maxAnchorLen = 160

// Comment describes one piece of user feedback anchored to a span of the plan.
type Comment struct {
	AnchorText string `json:"anchorText"`
	LineStart  int    `json:"lineStart"`
	LineEnd    int    `json:"lineEnd"`
	Body       string `json:"body"`
}

// ApplyFeedback returns a rewritten version of planPath's contents with each
// comment inserted as a `> 💬 FEEDBACK:` blockquote right after the markdown
// block containing its anchor. It does NOT write to disk — the caller is
// responsible for persisting the result (or shipping it via ExitPlanMode's
// updatedInput.plan, which is what the plan-reviewer hook does so the plan
// file's mtime is bumped exactly once, not twice).
//
// Anchoring strategy: find the line containing AnchorText nearest to LineStart.
// Walk forward from that line to the end of the containing block (blank line or EOF),
// then insert the feedback blockquote after that line with a surrounding blank line.
func ApplyFeedback(planPath string, comments []Comment) (string, error) {
	raw, err := os.ReadFile(planPath)
	if err != nil {
		return "", fmt.Errorf("read plan: %w", err)
	}
	lines := strings.Split(string(raw), "\n")

	// Process comments in reverse line order so inserts don't shift later anchors.
	ordered := append([]Comment(nil), comments...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].LineStart > ordered[j].LineStart
	})

	for _, c := range ordered {
		anchorLine := findAnchor(lines, c)
		if anchorLine < 0 {
			// Anchor not found; append at end as a fallback.
			lines = appendFeedback(lines, len(lines)-1, c.Body, c.AnchorText)
			continue
		}
		end := blockEnd(lines, anchorLine)
		lines = appendFeedback(lines, end, c.Body, c.AnchorText)
	}

	return strings.Join(lines, "\n"), nil
}

func findAnchor(lines []string, c Comment) int {
	if c.AnchorText == "" {
		return -1
	}
	// Normalize anchor to search one line at a time (anchor may span lines; use first line).
	anchor := c.AnchorText
	if nl := strings.Index(anchor, "\n"); nl > 0 {
		anchor = anchor[:nl]
	}
	anchor = strings.TrimSpace(anchor)
	if anchor == "" {
		return -1
	}

	// Prefer a match near the hinted line range.
	best := -1
	bestDist := 1 << 30
	hint := c.LineStart
	if hint <= 0 {
		hint = 0
	}
	for i, line := range lines {
		if !strings.Contains(line, anchor) {
			continue
		}
		d := i - hint
		if d < 0 {
			d = -d
		}
		if d < bestDist {
			best = i
			bestDist = d
		}
	}
	return best
}

func blockEnd(lines []string, start int) int {
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			return i - 1
		}
	}
	return len(lines) - 1
}

func appendFeedback(lines []string, afterIdx int, body, anchor string) []string {
	// Build the blockquote lines. Include the highlighted anchor snippet so
	// Claude can disambiguate which span the user was reacting to — anchor
	// position alone is often ambiguous when feedback is terse ("why?",
	// "unclear") or when the same phrasing appears in multiple places.
	header := "> 💬 FEEDBACK"
	if snippet := formatAnchorSnippet(anchor); snippet != "" {
		header += " on " + snippet
	}
	header += ": " + firstLine(body)
	bqLines := []string{header}
	rest := remainingLines(body)
	for _, l := range rest {
		bqLines = append(bqLines, "> "+l)
	}

	// Ensure exactly one blank line before and after.
	prefix := []string{""}
	if afterIdx >= 0 && afterIdx < len(lines) && strings.TrimSpace(lines[afterIdx]) == "" {
		prefix = nil
	}
	suffix := []string{""}
	if afterIdx+1 < len(lines) && strings.TrimSpace(lines[afterIdx+1]) == "" {
		suffix = nil
	}

	insert := append(append(prefix, bqLines...), suffix...)

	out := make([]string, 0, len(lines)+len(insert))
	out = append(out, lines[:afterIdx+1]...)
	out = append(out, insert...)
	out = append(out, lines[afterIdx+1:]...)
	return out
}

// formatAnchorSnippet renders the highlighted text for inclusion in the
// blockquote header. Collapses internal whitespace/newlines to single spaces,
// truncates to maxAnchorLen runes with an ellipsis, and swaps double quotes
// for single so the outer quoting doesn't collide with user content.
func formatAnchorSnippet(anchor string) string {
	anchor = strings.TrimSpace(anchor)
	if anchor == "" {
		return ""
	}
	anchor = strings.Join(strings.Fields(anchor), " ")
	if utf8.RuneCountInString(anchor) > maxAnchorLen {
		runes := []rune(anchor)
		anchor = strings.TrimRight(string(runes[:maxAnchorLen]), " ") + "…"
	}
	anchor = strings.ReplaceAll(anchor, `"`, `'`)
	return `"` + anchor + `"`
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return strings.TrimRight(s[:i], "\r")
	}
	return s
}

func remainingLines(s string) []string {
	i := strings.Index(s, "\n")
	if i < 0 {
		return nil
	}
	rest := s[i+1:]
	return strings.Split(strings.TrimRight(rest, "\n"), "\n")
}
