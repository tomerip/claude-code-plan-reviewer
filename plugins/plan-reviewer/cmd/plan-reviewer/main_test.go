package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestE2E_Approve and TestE2E_Feedback spawn the actual built binary and drive
// the HTTP interface to exercise the full PreToolUse hook cycle. These skip
// if the platform binary isn't built yet.
//
// Expected prior: `make build` has produced bin/<os>-<arch>/plan-reviewer.

// launcherPath returns the path to hooks/launch.sh — the real entry point
// users hit. Running through launch.sh exercises the .gz decompression path,
// and only skips if the gzipped binary for the current platform isn't built.
func launcherPath(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	// Require the gzipped binary so the launcher can decompress it.
	gz := filepath.Join(root, "bin", runtime.GOOS+"-"+runtime.GOARCH, "plan-reviewer.gz")
	if _, err := os.Stat(gz); err != nil {
		t.Skipf("gzipped binary not built at %s — run `make build-all` first", gz)
	}
	return filepath.Join(root, "hooks", "launch.sh")
}

func e2eSetup(t *testing.T) (binPath, planPath, transcriptPath, hookInput string, cleanup func()) {
	t.Helper()
	binPath = launcherPath(t)

	// Fake HOME so path-containment checks use a temp plansDir.
	home := t.TempDir()
	plansDir := filepath.Join(home, ".claude", "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatal(err)
	}
	planPath = filepath.Join(plansDir, "e2e-plan.md")
	plan := "# Test Plan\n\nFirst paragraph about Fastapi.\n\nSecond paragraph about something else.\n"
	if err := os.WriteFile(planPath, []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}

	transcriptPath = filepath.Join(t.TempDir(), "transcript.jsonl")
	transcript := fmt.Sprintf(
		`{"type":"attachment","attachment":{"type":"plan_mode","planFilePath":"%s","planExists":true}}`+"\n",
		planPath,
	)
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}

	hookIn := map[string]any{
		"session_id":      "e2e",
		"transcript_path": transcriptPath,
		"cwd":             "/tmp",
		"hook_event_name": "PreToolUse",
		"tool_name":       "ExitPlanMode",
		"tool_input":      map[string]any{},
	}
	hb, err := json.Marshal(hookIn)
	if err != nil {
		t.Fatal(err)
	}
	hookInput = string(hb)

	t.Setenv("HOME", home)
	t.Setenv("PLAN_REVIEWER_NO_BROWSER", "1")

	cleanup = func() {}
	return
}

// runBinary spawns the binary with stdin=hookInput and returns a live process
// we can drive via HTTP before it completes.
func runBinary(t *testing.T, binPath, hookInput string) (cmd *exec.Cmd, port int, stdoutBuf *syncBuffer, done chan error) {
	t.Helper()
	cmd = exec.Command("bash", binPath)
	// launch.sh needs CLAUDE_PLUGIN_ROOT; compute it from binPath (hooks/launch.sh).
	pluginRoot := filepath.Dir(filepath.Dir(binPath))
	cmd.Env = append(os.Environ(),
		"HOME="+os.Getenv("HOME"),
		"PLAN_REVIEWER_NO_BROWSER=1",
		"CLAUDE_PLUGIN_ROOT="+pluginRoot,
	)
	cmd.Stdin = strings.NewReader(hookInput)

	stdoutBuf = &syncBuffer{}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = stdoutBuf

	done = make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()

	// Parse the port from stderr log "review UI on http://127.0.0.1:PORT".
	portRe := regexp.MustCompile(`127\.0\.0\.1:(\d+)`)
	scanner := bufio.NewScanner(stderrPipe)
	deadline := time.After(5 * time.Second)
	portCh := make(chan int, 1)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if m := portRe.FindStringSubmatch(line); m != nil {
				p := 0
				fmt.Sscanf(m[1], "%d", &p)
				portCh <- p
				return
			}
		}
	}()
	select {
	case port = <-portCh:
	case <-deadline:
		_ = cmd.Process.Kill()
		t.Fatalf("never saw port in stderr within 5s")
	}
	return
}

// syncBuffer is a thread-safe buffer.
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

func fetchCSRF(t *testing.T, port int) string {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`__CSRF_TOKEN = "([a-f0-9]+)"`).FindStringSubmatch(string(body))
	if m == nil {
		t.Fatalf("no CSRF token in response:\n%s", string(body))
	}
	return m[1]
}

func submit(t *testing.T, port int, token, body string) {
	t.Helper()
	req, err := http.NewRequest("POST",
		fmt.Sprintf("http://127.0.0.1:%d/submit", port),
		strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("submit failed: %d %s", resp.StatusCode, b)
	}
}

func TestE2E_Approve(t *testing.T) {
	binPath, planPath, _, hookInput, cleanup := e2eSetup(t)
	defer cleanup()

	origPlan, _ := os.ReadFile(planPath)

	_, port, stdoutBuf, done := runBinary(t, binPath, hookInput)
	token := fetchCSRF(t, port)
	submit(t, port, token, `{"action":"approve","comments":[]}`)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("binary exited with error: %v\nstdout: %s", err, stdoutBuf.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("binary did not exit within 5s")
	}

	// Stdout should be an "allow" JSON decision.
	var parsed struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdoutBuf.String()), &parsed); err != nil {
		t.Fatalf("stdout is not JSON: %s", stdoutBuf.String())
	}
	if parsed.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("want allow, got %s", parsed.HookSpecificOutput.PermissionDecision)
	}

	// Plan file must be unchanged.
	after, _ := os.ReadFile(planPath)
	if string(after) != string(origPlan) {
		t.Errorf("plan file changed on approve:\nbefore: %s\nafter:  %s", origPlan, after)
	}
}

func TestE2E_Feedback(t *testing.T) {
	binPath, planPath, _, hookInput, cleanup := e2eSetup(t)
	defer cleanup()

	_, port, stdoutBuf, done := runBinary(t, binPath, hookInput)
	token := fetchCSRF(t, port)
	submit(t, port, token, `{"action":"feedback","comments":[{"anchorText":"Fastapi","lineStart":3,"lineEnd":3,"body":"why not flask"}]}`)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("binary exited with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("binary did not exit within 5s")
	}

	var parsed struct {
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdoutBuf.String()), &parsed); err != nil {
		t.Fatalf("stdout is not JSON: %s", stdoutBuf.String())
	}
	if parsed.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("want deny, got %s", parsed.HookSpecificOutput.PermissionDecision)
	}
	if !strings.Contains(parsed.HookSpecificOutput.PermissionDecisionReason, "FEEDBACK") {
		t.Errorf("reason should mention FEEDBACK: %s", parsed.HookSpecificOutput.PermissionDecisionReason)
	}

	// Plan file must contain the feedback blockquote.
	after, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "> 💬 FEEDBACK: why not flask") {
		t.Errorf("feedback not written to plan:\n%s", after)
	}
}

func TestE2E_Cancel_OnTabClose(t *testing.T) {
	// Simulates the browser tab being closed: sendBeacon-style POST to
	// /cancel with the CSRF token in the body. Binary should exit with an
	// "ask" decision so Claude Code falls back to its normal approval flow.
	binPath, planPath, _, hookInput, cleanup := e2eSetup(t)
	defer cleanup()

	origPlan, _ := os.ReadFile(planPath)

	_, port, stdoutBuf, done := runBinary(t, binPath, hookInput)
	token := fetchCSRF(t, port)

	// sendBeacon ships the token in the JSON body, not a header.
	req, err := http.NewRequest("POST",
		fmt.Sprintf("http://127.0.0.1:%d/cancel", port),
		strings.NewReader(fmt.Sprintf(`{"token":%q}`, token)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel POST failed: %d", resp.StatusCode)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("binary exited with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("binary did not exit within 5s after cancel")
	}

	var parsed struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal([]byte(stdoutBuf.String()), &parsed); err != nil {
		t.Fatalf("stdout is not JSON: %s", stdoutBuf.String())
	}
	if parsed.HookSpecificOutput.PermissionDecision != "ask" {
		t.Errorf("want ask, got %s (stdout: %s)",
			parsed.HookSpecificOutput.PermissionDecision, stdoutBuf.String())
	}

	// Plan file must be unchanged.
	after, _ := os.ReadFile(planPath)
	if string(after) != string(origPlan) {
		t.Errorf("plan file changed on cancel:\nbefore: %s\nafter:  %s", origPlan, after)
	}
}
