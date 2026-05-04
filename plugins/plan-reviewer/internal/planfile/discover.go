package planfile

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Discover returns the plan file path the user is currently reviewing.
//
// Strategy:
//  1. Walk the transcript (JSONL) in reverse, looking for an attachment with
//     type="plan_mode" and a planFilePath field. This is the authoritative source.
//  2. Fallback: most-recently-modified *.md in ~/.claude/plans/ if its mtime
//     is within the last 5 minutes.
//
// Every candidate is validated to sit under ~/.claude/plans/ with a .md
// extension, to resolve (via EvalSymlinks) within the same directory, and to
// be a regular file. This blocks a prompt-injected transcript from pointing
// at /etc/hosts, ~/.ssh/authorized_keys, or a symlink that escapes plansDir.
//
// Returns "" if neither works. The caller should emit an "ask" decision in that case.
func Discover(transcriptPath string) string {
	plansDir, err := plansDir()
	if err != nil {
		return ""
	}
	if p := fromTranscript(transcriptPath); p != "" {
		if v, ok := validate(p, plansDir); ok {
			return v
		}
	}
	return fromPlansDirMtime(plansDir)
}

type transcriptLine struct {
	Type       string `json:"type"`
	Attachment *struct {
		Type         string `json:"type"`
		PlanFilePath string `json:"planFilePath"`
	} `json:"attachment,omitempty"`
}

func fromTranscript(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	// JSONL transcripts can be large; collect all plan_mode attachments and take the last.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	var last string
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var l transcriptLine
		if err := json.Unmarshal(line, &l); err != nil {
			continue
		}
		if l.Attachment != nil && l.Attachment.Type == "plan_mode" && l.Attachment.PlanFilePath != "" {
			last = l.Attachment.PlanFilePath
		}
	}
	return last
}

func fromPlansDirMtime(plansDir string) string {
	entries, err := os.ReadDir(plansDir)
	if err != nil {
		return ""
	}

	type planEntry struct {
		path  string
		mtime time.Time
	}
	var plans []planEntry
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		plans = append(plans, planEntry{
			path:  filepath.Join(plansDir, e.Name()),
			mtime: info.ModTime(),
		})
	}
	if len(plans) == 0 {
		return ""
	}

	sort.Slice(plans, func(i, j int) bool {
		return plans[i].mtime.After(plans[j].mtime)
	})

	if time.Since(plans[0].mtime) > 5*time.Minute {
		return ""
	}
	if v, ok := validate(plans[0].path, plansDir); ok {
		return v
	}
	return ""
}

func plansDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "plans"), nil
}

// validate constrains a candidate path:
//   - absolute path
//   - .md extension
//   - symlinks resolve within plansDir
//   - refers to a regular file (no devices, sockets, dirs)
//
// Returns the resolved absolute path on success.
func validate(candidate, plansDir string) (string, bool) {
	if candidate == "" {
		return "", false
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", false
	}
	if filepath.Ext(abs) != ".md" {
		return "", false
	}

	plansAbs, err := filepath.Abs(plansDir)
	if err != nil {
		return "", false
	}
	// Resolve symlinks so we check the real target.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", false
	}
	resolvedPlansDir, err := filepath.EvalSymlinks(plansAbs)
	if err != nil {
		// plansDir itself must exist.
		return "", false
	}
	rel, err := filepath.Rel(resolvedPlansDir, resolved)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || strings.Contains(rel, string(filepath.Separator)) {
		return "", false
	}

	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return resolved, true
}
