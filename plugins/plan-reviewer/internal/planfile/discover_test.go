package planfile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setupFakeHome creates a temp directory that we use as $HOME for the test,
// with a .claude/plans subdirectory. Returns the plansDir path.
func setupFakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	plans := filepath.Join(home, ".claude", "plans")
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	return plans
}

func writePlan(t *testing.T, plansDir, name, content string) string {
	t.Helper()
	p := filepath.Join(plansDir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiscover_FromTranscript(t *testing.T) {
	plans := setupFakeHome(t)
	planPath := writePlan(t, plans, "my-plan.md", "# hi")

	transcript := writeTranscript(t,
		`{"type":"attachment","attachment":{"type":"plan_mode","planFilePath":"`+planPath+`","planExists":true}}`,
	)
	got := Discover(transcript)
	if got == "" {
		t.Fatal("expected discovery to succeed")
	}
	// EvalSymlinks may prepend /private on macOS; resolve both for comparison.
	wantResolved, _ := filepath.EvalSymlinks(planPath)
	if got != wantResolved {
		t.Errorf("got %q, want %q", got, wantResolved)
	}
}

func TestDiscover_RejectsOutsidePlansDir(t *testing.T) {
	_ = setupFakeHome(t)
	// Transcript references a file outside the plans dir.
	outside := filepath.Join(t.TempDir(), "victim.md")
	if err := os.WriteFile(outside, []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}
	transcript := writeTranscript(t,
		`{"type":"attachment","attachment":{"type":"plan_mode","planFilePath":"`+outside+`"}}`,
	)
	got := Discover(transcript)
	if got != "" {
		t.Errorf("expected empty result for outside-plansDir path, got %q", got)
	}
}

func TestDiscover_RejectsNonMdExtension(t *testing.T) {
	plans := setupFakeHome(t)
	badPath := writePlan(t, plans, "notes.txt", "data")
	transcript := writeTranscript(t,
		`{"type":"attachment","attachment":{"type":"plan_mode","planFilePath":"`+badPath+`"}}`,
	)
	got := Discover(transcript)
	if got != "" {
		t.Errorf("expected empty for .txt, got %q", got)
	}
}

func TestDiscover_RejectsSymlinkEscape(t *testing.T) {
	plans := setupFakeHome(t)
	// Create a file outside plansDir, then a symlink inside plansDir pointing to it.
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(plans, "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlinks not supported")
	}
	transcript := writeTranscript(t,
		`{"type":"attachment","attachment":{"type":"plan_mode","planFilePath":"`+link+`"}}`,
	)
	got := Discover(transcript)
	if got != "" {
		t.Errorf("expected empty for symlink-escaping plansDir, got %q", got)
	}
}

func TestDiscover_MtimeFallback(t *testing.T) {
	plans := setupFakeHome(t)
	_ = writePlan(t, plans, "old.md", "old")
	newPlan := writePlan(t, plans, "new.md", "new")
	// Make old.md actually old.
	oldTime := time.Now().Add(-1 * time.Hour)
	_ = os.Chtimes(filepath.Join(plans, "old.md"), oldTime, oldTime)

	// Empty/missing transcript → mtime branch.
	got := Discover("")
	if got == "" {
		t.Fatal("expected mtime fallback to return the newest plan")
	}
	wantResolved, _ := filepath.EvalSymlinks(newPlan)
	if got != wantResolved {
		t.Errorf("got %q, want %q", got, wantResolved)
	}
}

func TestDiscover_MtimeFallback_StaleRejected(t *testing.T) {
	plans := setupFakeHome(t)
	p := writePlan(t, plans, "stale.md", "stale")
	// Make the file > 5 minutes old.
	old := time.Now().Add(-10 * time.Minute)
	_ = os.Chtimes(p, old, old)

	got := Discover("")
	if got != "" {
		t.Errorf("expected empty for stale plan, got %q", got)
	}
}

func TestDiscover_EmptyTranscriptPath(t *testing.T) {
	_ = setupFakeHome(t)
	// No transcript, no plans dir content → must return empty.
	got := Discover("")
	if got != "" {
		t.Errorf("expected empty when no plans exist, got %q", got)
	}
}
