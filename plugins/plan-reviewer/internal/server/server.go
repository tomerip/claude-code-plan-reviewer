package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"plan-reviewer/internal/hook"
	"plan-reviewer/internal/planfile"
	"plan-reviewer/internal/render"
)

// Assets wraps the filesystem that holds the embedded web/ tree. Using fs.FS
// (not embed.FS directly) lets tests substitute an in-memory fstest.MapFS.
type Assets struct {
	FS fs.FS
}

type Payload struct {
	PlanPath string
	PlanText string
	Theme    string
}

// Actions delivered on Server.Done. "cancel" means the browser reported the
// tab was closed (via pagehide sendBeacon) or heartbeats stopped arriving,
// so the caller should degrade to Claude Code's default approval flow.
const (
	ActionApprove  = "approve"
	ActionFeedback = "feedback"
	ActionCancel   = "cancel"

	maxSubmitBodyBytes = 1 << 20 // 1 MiB
)

type Result struct {
	Action   string
	Comments []planfile.Comment
}

// Config tunes heartbeat behavior. Zero values mean "use defaults".
type Config struct {
	// HeartbeatTimeout is the maximum time between /heartbeat requests before
	// the server assumes the browser is gone. Default: 15s.
	HeartbeatTimeout time.Duration
	// HeartbeatCheckInterval is how often the monitor goroutine wakes. Default: 3s.
	HeartbeatCheckInterval time.Duration
}

func (c Config) withDefaults() Config {
	if c.HeartbeatTimeout == 0 {
		c.HeartbeatTimeout = 15 * time.Second
	}
	if c.HeartbeatCheckInterval == 0 {
		c.HeartbeatCheckInterval = 3 * time.Second
	}
	return c
}

type Server struct {
	httpSrv   *http.Server
	Port      int
	Done      chan Result
	csrfToken string
	origin    string
	cfg       Config

	// Heartbeat tracking.
	hbMu            sync.Mutex
	hbReceived      bool
	lastHeartbeat   time.Time
	monitorCtx      context.Context
	monitorCancel   context.CancelFunc
	resultSent      bool // true once any action has been pushed onto Done

	shutdownOnce sync.Once
}

// Start binds to an ephemeral 127.0.0.1 port and returns immediately.
// Caller reads from Done exactly once.
func Start(assets Assets, payload Payload) (*Server, error) {
	return StartWithConfig(assets, payload, Config{})
}

// StartWithConfig is like Start but lets tests tune heartbeat timings.
func StartWithConfig(assets Assets, payload Payload, cfg Config) (*Server, error) {
	cfg = cfg.withDefaults()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	token, err := randomToken()
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("generate csrf token: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		Port:          port,
		Done:          make(chan Result, 1),
		csrfToken:     token,
		origin:        fmt.Sprintf("http://127.0.0.1:%d", port),
		cfg:           cfg,
		monitorCtx:    ctx,
		monitorCancel: cancel,
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux, assets, payload)

	s.httpSrv = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	go func() {
		_ = s.httpSrv.Serve(listener)
	}()

	go s.heartbeatMonitor()

	return s, nil
}

func (s *Server) Shutdown() {
	s.shutdownOnce.Do(func() {
		s.monitorCancel()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.httpSrv.Shutdown(ctx); err != nil {
			hook.Logf("server shutdown: %v", err)
		}
	})
}

func (s *Server) registerRoutes(mux *http.ServeMux, assets Assets, payload Payload) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			s.serveStatic(w, r, assets)
			return
		}
		s.serveIndex(w, r, assets, payload)
	})

	mux.HandleFunc("/submit", s.handleSubmit)
	mux.HandleFunc("/heartbeat", s.handleHeartbeat)
	mux.HandleFunc("/cancel", s.handleCancel)
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !s.checkOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !s.checkCSRFHeader(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Action   string             `json:"action"`
		Comments []planfile.Comment `json:"comments"`
	}
	// Cap the request body so a compromised tab (or a local process that
	// has scraped the CSRF token) can't pin the process on a multi-GB
	// upload. 1 MiB is well above any realistic review payload.
	r.Body = http.MaxBytesReader(w, r.Body, maxSubmitBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Try to publish the result BEFORE responding. If sendResult returns
	// false, some other path (heartbeat-timeout cancel, a concurrent
	// /cancel) already won — we must not tell the browser "approved" in
	// that case. Return 409 so the frontend can show the cancelled state.
	if !s.sendResult(Result{Action: body.Action, Comments: body.Comments}) {
		http.Error(w, "review already concluded", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !s.checkOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !s.checkCSRFHeader(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.hbMu.Lock()
	s.hbReceived = true
	s.lastHeartbeat = time.Now()
	s.hbMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// handleCancel accepts the CSRF token in either the X-CSRF-Token header OR
// a JSON body {"token": "..."}. sendBeacon, used by the browser's pagehide
// handler, can't set custom headers — only Content-Type — so the body-token
// path is what gets used in practice on tab close.
func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !s.checkOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	// Body is optional — just decode best-effort.
	_ = json.NewDecoder(r.Body).Decode(&body)

	supplied := r.Header.Get("X-CSRF-Token")
	if supplied == "" {
		supplied = body.Token
	}
	if subtle.ConstantTimeCompare([]byte(supplied), []byte(s.csrfToken)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	w.WriteHeader(http.StatusNoContent)
	s.sendResult(Result{Action: ActionCancel})
}

// heartbeatMonitor fires a cancel Result if heartbeats stop arriving after at
// least one was received. Never fires before the first heartbeat so a browser
// that hasn't loaded yet (or a minimal client that never heartbeats) won't be
// cancelled prematurely.
func (s *Server) heartbeatMonitor() {
	ticker := time.NewTicker(s.cfg.HeartbeatCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.monitorCtx.Done():
			return
		case <-ticker.C:
			s.hbMu.Lock()
			stale := s.hbReceived && time.Since(s.lastHeartbeat) > s.cfg.HeartbeatTimeout
			s.hbMu.Unlock()
			if stale {
				s.sendResult(Result{Action: ActionCancel})
				return
			}
		}
	}
}

// sendResult pushes a Result onto Done exactly once. Returns true if this
// caller's Result was published, false if a prior sendResult already won.
// The Done channel is buffered=1; non-blocking send is safe because the
// resultSent guard prevents any second send from reaching the channel.
func (s *Server) sendResult(r Result) bool {
	s.hbMu.Lock()
	if s.resultSent {
		s.hbMu.Unlock()
		return false
	}
	s.resultSent = true
	s.hbMu.Unlock()

	select {
	case s.Done <- r:
	default:
	}
	return true
}

func (s *Server) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	return origin == s.origin
}

func (s *Server) checkCSRFHeader(r *http.Request) bool {
	supplied := r.Header.Get("X-CSRF-Token")
	return subtle.ConstantTimeCompare([]byte(supplied), []byte(s.csrfToken)) == 1
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request, assets Assets) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	data, err := fs.ReadFile(assets.FS, "web/"+name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := "application/octet-stream"
	switch filepath.Ext(name) {
	case ".css":
		ct = "text/css; charset=utf-8"
	case ".js":
		ct = "application/javascript; charset=utf-8"
	case ".html":
		ct = "text/html; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(data)
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request, assets Assets, payload Payload) {
	tmplBytes, err := fs.ReadFile(assets.FS, "web/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl := string(tmplBytes)

	rendered := render.Render(payload.PlanText)

	var toc strings.Builder
	for _, h := range rendered.Headings {
		fmt.Fprintf(&toc, `<a href="#%s" class="pr-toc-h%d">%s</a>`,
			html.EscapeString(h.ID), h.Level, html.EscapeString(h.Text))
	}

	filename := filepath.Base(payload.PlanPath)

	// JSON-encode the plan path so it's safe to interpolate inside an inline
	// <script> (a bare "</script>" in the path would otherwise close the tag).
	planPathJSON, err := json.Marshal(payload.PlanPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	csrfJSON, _ := json.Marshal(s.csrfToken)

	pairs := [][2]string{
		{"{{THEME}}", validateTheme(payload.Theme)},
		{"{{FILENAME}}", html.EscapeString(filename)},
		{"{{PLAN_HTML}}", rendered.HTML},
		{"{{TOC_HTML}}", toc.String()},
		{"{{PLAN_PATH_JSON}}", string(planPathJSON)},
		{"{{CSRF_TOKEN_JSON}}", string(csrfJSON)},
		{"{{PLAN_TEXT_LEN}}", fmt.Sprintf("%d", len(payload.PlanText))},
	}
	for _, p := range pairs {
		tmpl = strings.ReplaceAll(tmpl, p[0], p[1])
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(tmpl))
}

// validateTheme returns the supplied theme name if it matches a known Claude
// Code theme, else "dark". The returned value lands in an HTML attribute, so
// restricting to an allowlist eliminates the risk of attribute injection.
func validateTheme(s string) string {
	if s == "" {
		return "dark"
	}
	s = strings.ToLower(s)
	allowed := map[string]bool{
		"dark": true, "light": true,
		"dark-daltonized": true, "light-daltonized": true,
		"dark-ansi": true, "light-ansi": true,
	}
	if allowed[s] {
		return s
	}
	return "dark"
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
