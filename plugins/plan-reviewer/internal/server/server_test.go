package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// fakeAssets builds a minimal in-memory web/ tree with the placeholders the
// server expects. Using fstest.MapFS keeps tests hermetic.
func fakeAssets() Assets {
	return Assets{FS: fstest.MapFS{
		"web/index.html": &fstest.MapFile{Data: []byte(`<!DOCTYPE html>
<html data-theme="{{THEME}}">
<head><title>{{FILENAME}}</title></head>
<body>
<nav>{{TOC_HTML}}</nav>
<main>{{PLAN_HTML}}</main>
<script>
window.__PLAN_PATH = {{PLAN_PATH_JSON}};
window.__CSRF_TOKEN = {{CSRF_TOKEN_JSON}};
</script>
</body>
</html>
`)},
		"web/app.js":    &fstest.MapFile{Data: []byte(`console.log("test app");`)},
		"web/style.css": &fstest.MapFile{Data: []byte(`body { color: red; }`)},
	}}
}

func buildServer(t *testing.T, planText string) *Server {
	t.Helper()
	s, err := Start(fakeAssets(), Payload{
		PlanPath: "/tmp/plan.md",
		PlanText: planText,
		Theme:    "dark",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	return s
}

func postSubmit(t *testing.T, s *Server, token, body string, opts ...func(*http.Request)) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/submit", s.Port),
		bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-CSRF-Token", token)
	}
	for _, o := range opts {
		o(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSubmit_MissingCSRF(t *testing.T) {
	s := buildServer(t, "# x")
	resp := postSubmit(t, s, "", `{"action":"approve","comments":[]}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403, got %d", resp.StatusCode)
	}
}

func TestSubmit_WrongCSRF(t *testing.T) {
	s := buildServer(t, "# x")
	resp := postSubmit(t, s, "wrong", `{"action":"approve","comments":[]}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403, got %d", resp.StatusCode)
	}
}

func TestSubmit_ValidCSRF_Approve(t *testing.T) {
	s := buildServer(t, "# x")
	resp := postSubmit(t, s, s.csrfToken, `{"action":"approve","comments":[]}`)
	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
	select {
	case r := <-s.Done:
		if r.Action != "approve" {
			t.Errorf("want approve, got %s", r.Action)
		}
	default:
		t.Error("expected result on Done channel")
	}
}

func TestSubmit_ValidCSRF_Feedback(t *testing.T) {
	s := buildServer(t, "# x")
	body := `{"action":"feedback","comments":[{"anchorText":"x","lineStart":1,"lineEnd":1,"body":"note"}]}`
	resp := postSubmit(t, s, s.csrfToken, body)
	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	select {
	case r := <-s.Done:
		if r.Action != "feedback" || len(r.Comments) != 1 || r.Comments[0].Body != "note" {
			t.Errorf("unexpected result: %+v", r)
		}
	default:
		t.Error("expected result on Done channel")
	}
}

func TestSubmit_WrongOrigin(t *testing.T) {
	s := buildServer(t, "# x")
	resp := postSubmit(t, s, s.csrfToken, `{"action":"approve","comments":[]}`,
		func(r *http.Request) { r.Header.Set("Origin", "http://evil.example.com") },
	)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for wrong Origin, got %d", resp.StatusCode)
	}
}

func TestSubmit_MatchingOrigin(t *testing.T) {
	s := buildServer(t, "# x")
	resp := postSubmit(t, s, s.csrfToken, `{"action":"approve","comments":[]}`,
		func(r *http.Request) { r.Header.Set("Origin", fmt.Sprintf("http://127.0.0.1:%d", s.Port)) },
	)
	if resp.StatusCode != 200 {
		t.Errorf("want 200 for matching Origin, got %d", resp.StatusCode)
	}
	<-s.Done
}

func TestSubmit_GET_MethodNotAllowed(t *testing.T) {
	s := buildServer(t, "# x")
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/submit", s.Port))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", resp.StatusCode)
	}
}

func TestServeIndex_RendersPlan(t *testing.T) {
	s := buildServer(t, "# Hello\n\nA paragraph.")
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", s.Port))
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Hello") {
		t.Errorf("plan heading not rendered: %s", body)
	}
	if !strings.Contains(body, `data-theme="dark"`) {
		t.Errorf("theme not injected: %s", body)
	}
	// CSRF token should be a JSON-quoted hex string.
	tokenRe := regexp.MustCompile(`__CSRF_TOKEN = "([a-f0-9]+)"`)
	m := tokenRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("csrf token not in expected form: %s", body)
	}
	if m[1] != s.csrfToken {
		t.Errorf("token mismatch: got %q vs server %q", m[1], s.csrfToken)
	}
	// Plan path should be JSON-encoded (quoted).
	if !strings.Contains(body, `__PLAN_PATH = "/tmp/plan.md"`) {
		t.Errorf("plan path not JSON-encoded: %s", body)
	}
}

func TestServeIndex_PlanPathWithScriptTag_Escaped(t *testing.T) {
	// A plan path containing </script> must not break out of the inline script.
	s, err := Start(fakeAssets(), Payload{
		PlanPath: `/tmp/</script><img src=x>.md`,
		PlanText: "# x",
		Theme:    "dark",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", s.Port))
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	// Raw </script> must not appear in the rendered output — json.Marshal
	// escapes the `<` in strings to < by default, closing this hole.
	if strings.Contains(body, `"/tmp/</script>`) {
		t.Errorf("plan path was not safely escaped inside <script>: %s", body)
	}
}

func TestServeStatic_CSS(t *testing.T) {
	s := buildServer(t, "# x")
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/style.css", s.Port))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("wrong content type: %s", ct)
	}
}

func TestServeStatic_JS(t *testing.T) {
	s := buildServer(t, "# x")
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/app.js", s.Port))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("wrong content type: %s", ct)
	}
}

func TestServeStatic_NotFound(t *testing.T) {
	s := buildServer(t, "# x")
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/nope", s.Port))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func TestValidateTheme(t *testing.T) {
	cases := map[string]string{
		"":                 "dark",
		"garbage":          "dark",
		"dark":             "dark",
		"light":            "light",
		"DARK":             "dark",
		"dark-ansi":        "dark-ansi",
		"light-daltonized": "light-daltonized",
		"<script>":         "dark",
	}
	for in, want := range cases {
		if got := validateTheme(in); got != want {
			t.Errorf("validateTheme(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRandomToken_HexAndLong(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		tok, err := randomToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) != 64 {
			t.Errorf("token length %d, want 64", len(tok))
		}
		if seen[tok] {
			t.Errorf("duplicate token")
		}
		seen[tok] = true
	}
}

// --- heartbeat + cancel ---

func postCancelBody(t *testing.T, s *Server, token string) *http.Response {
	t.Helper()
	body := fmt.Sprintf(`{"token":%q}`, token)
	req, err := http.NewRequest("POST",
		fmt.Sprintf("http://127.0.0.1:%d/cancel", s.Port),
		bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestCancel_ValidTokenInBody(t *testing.T) {
	s := buildServer(t, "# x")
	resp := postCancelBody(t, s, s.csrfToken)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("want 204, got %d", resp.StatusCode)
	}
	select {
	case r := <-s.Done:
		if r.Action != ActionCancel {
			t.Errorf("want cancel, got %s", r.Action)
		}
	case <-time.After(1 * time.Second):
		t.Error("expected cancel on Done channel")
	}
}

func TestCancel_WrongTokenInBody(t *testing.T) {
	s := buildServer(t, "# x")
	resp := postCancelBody(t, s, "nope")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403, got %d", resp.StatusCode)
	}
	select {
	case <-s.Done:
		t.Error("should not have received Done signal")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestCancel_ValidTokenInHeader(t *testing.T) {
	// The header path is used by the fetch() fallback when sendBeacon isn't
	// available. Belt-and-braces: it must also work.
	s := buildServer(t, "# x")
	req, _ := http.NewRequest("POST",
		fmt.Sprintf("http://127.0.0.1:%d/cancel", s.Port),
		bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", s.csrfToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("want 204, got %d", resp.StatusCode)
	}
	<-s.Done
}

func TestCancel_WrongOrigin(t *testing.T) {
	s := buildServer(t, "# x")
	body := fmt.Sprintf(`{"token":%q}`, s.csrfToken)
	req, _ := http.NewRequest("POST",
		fmt.Sprintf("http://127.0.0.1:%d/cancel", s.Port),
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403, got %d", resp.StatusCode)
	}
}

func TestHeartbeat_OK(t *testing.T) {
	s := buildServer(t, "# x")
	req, _ := http.NewRequest("POST",
		fmt.Sprintf("http://127.0.0.1:%d/heartbeat", s.Port),
		bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", s.csrfToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
}

func TestHeartbeat_MissingCSRF(t *testing.T) {
	s := buildServer(t, "# x")
	req, _ := http.NewRequest("POST",
		fmt.Sprintf("http://127.0.0.1:%d/heartbeat", s.Port),
		bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403, got %d", resp.StatusCode)
	}
}

func TestHeartbeatMonitor_CancelsOnTimeout(t *testing.T) {
	// Short timings so the test is fast.
	s, err := StartWithConfig(fakeAssets(), Payload{
		PlanPath: "/tmp/plan.md", PlanText: "# x", Theme: "dark",
	}, Config{
		HeartbeatTimeout:       100 * time.Millisecond,
		HeartbeatCheckInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)

	// Send one heartbeat, then stop.
	req, _ := http.NewRequest("POST",
		fmt.Sprintf("http://127.0.0.1:%d/heartbeat", s.Port),
		bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", s.csrfToken)
	if _, err := http.DefaultClient.Do(req); err != nil {
		t.Fatal(err)
	}

	select {
	case r := <-s.Done:
		if r.Action != ActionCancel {
			t.Errorf("want cancel, got %s", r.Action)
		}
	case <-time.After(2 * time.Second):
		t.Error("heartbeat timeout didn't fire cancel")
	}
}

func TestHeartbeatMonitor_NoCancelBeforeFirstHeartbeat(t *testing.T) {
	// If no heartbeat ever arrives, the monitor must NOT cancel — that would
	// ambush a user whose browser is slow to load.
	s, err := StartWithConfig(fakeAssets(), Payload{
		PlanPath: "/tmp/plan.md", PlanText: "# x", Theme: "dark",
	}, Config{
		HeartbeatTimeout:       50 * time.Millisecond,
		HeartbeatCheckInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)

	select {
	case r := <-s.Done:
		t.Errorf("unexpected cancel without any heartbeat: %+v", r)
	case <-time.After(300 * time.Millisecond):
		// expected — stayed quiet
	}
}

func TestSendResult_Idempotent(t *testing.T) {
	// Duplicate cancel signals (e.g. sendBeacon + heartbeat timeout firing
	// together) must result in exactly one delivery on Done.
	s := buildServer(t, "# x")
	if !s.sendResult(Result{Action: ActionApprove}) {
		t.Error("first sendResult should win")
	}
	if s.sendResult(Result{Action: ActionCancel}) {
		t.Error("second sendResult should return false")
	}

	select {
	case r := <-s.Done:
		if r.Action != ActionApprove {
			t.Errorf("want first (approve) to win, got %s", r.Action)
		}
	default:
		t.Error("first send was lost")
	}
	// Confirm the second did nothing.
	select {
	case r := <-s.Done:
		t.Errorf("duplicate send leaked: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSubmit_ReturnsConflictAfterCancel(t *testing.T) {
	// If a cancel/timeout already published a Result, a subsequent /submit
	// must get 409 — never 200. Otherwise the browser would show "approved"
	// while the Go side takes the cancel path.
	s := buildServer(t, "# x")
	// Simulate cancel winning first.
	if !s.sendResult(Result{Action: ActionCancel}) {
		t.Fatal("setup: cancel should win")
	}
	resp := postSubmit(t, s, s.csrfToken, `{"action":"approve","comments":[]}`)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("want 409, got %d", resp.StatusCode)
	}
	// Drain cancel so cleanup doesn't hang.
	<-s.Done
}

func TestShutdown_Idempotent(t *testing.T) {
	// Calling Shutdown twice must be a no-op, not a crash / spurious log.
	s, err := Start(fakeAssets(), Payload{
		PlanPath: "/tmp/plan.md", PlanText: "# x", Theme: "dark",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Shutdown()
	s.Shutdown() // must not panic
	s.Shutdown() // still must not panic
}
