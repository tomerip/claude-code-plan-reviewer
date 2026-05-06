package planfile

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

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
	// Build the blockquote lines.
	bqLines := []string{"> 💬 FEEDBACK: " + firstLine(body)}
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
