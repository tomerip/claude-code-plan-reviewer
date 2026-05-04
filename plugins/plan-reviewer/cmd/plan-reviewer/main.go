package main

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"plan-reviewer/internal/hook"
	"plan-reviewer/internal/planfile"
	"plan-reviewer/internal/server"
	"plan-reviewer/internal/theme"
)

//go:embed web/*
var assetsFS embed.FS

const reviewTimeout = 10 * time.Minute

func main() {
	// Support `--help` without a hook payload.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--help", "-h":
			fmt.Println("plan-reviewer — internal binary for the plan-reviewer Claude Code plugin.")
			fmt.Println("Reads a PreToolUse hook payload on stdin and emits a permission decision on stdout.")
			return
		case "--version":
			fmt.Println("plan-reviewer 0.1.0")
			return
		}
	}

	if err := run(); err != nil {
		hook.Logf("fatal: %v", err)
		// Always degrade to "ask" on failure so we don't break plan mode.
		_ = hook.WriteAsk(os.Stdout, fmt.Sprintf("plan-reviewer error: %v", err))
		os.Exit(0)
	}
}

func run() error {
	in, err := hook.Read(os.Stdin)
	if err != nil {
		return err
	}

	planPath := planfile.Discover(in.TranscriptPath)
	if planPath == "" {
		hook.Logf("could not discover plan file; degrading to ask")
		return hook.WriteAsk(os.Stdout, "plan-reviewer: could not locate plan file")
	}

	planBytes, err := os.ReadFile(planPath)
	if err != nil {
		return fmt.Errorf("read plan: %w", err)
	}
	initialStat, err := os.Stat(planPath)
	if err != nil {
		return fmt.Errorf("stat plan: %w", err)
	}

	srv, err := server.Start(
		server.Assets{FS: assetsFS},
		server.Payload{
			PlanPath: planPath,
			PlanText: string(planBytes),
			Theme:    theme.Detect(),
		},
	)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("http://127.0.0.1:%d", srv.Port)
	hook.Logf("review UI on %s (plan: %s)", url, planPath)
	if os.Getenv("PLAN_REVIEWER_NO_BROWSER") == "" {
		openBrowser(url)
	}

	var result server.Result
	select {
	case result = <-srv.Done:
	case <-time.After(reviewTimeout):
		srv.Shutdown()
		return hook.WriteAsk(os.Stdout, "plan-reviewer: review timed out")
	}

	srv.Shutdown()

	switch result.Action {
	case server.ActionApprove:
		return hook.WriteAllow(os.Stdout)
	case server.ActionFeedback:
		// Warn if the plan was modified while the browser was open. Anchors may
		// not line up with the current content, in which case ApplyFeedback's
		// fallback silently appends to EOF. We still write — losing the feedback
		// is worse than a noisy warning.
		if cur, err := os.Stat(planPath); err == nil && !cur.ModTime().Equal(initialStat.ModTime()) {
			hook.Logf("warning: plan file mtime changed during review; anchors may drift")
		}
		if err := planfile.ApplyFeedback(planPath, result.Comments); err != nil {
			return err
		}
		reason := "Review complete — user requested revisions. Re-read the plan file, address each `> 💬 FEEDBACK:` annotation, remove the FEEDBACK blockquotes, then call ExitPlanMode again."
		return hook.WriteDeny(os.Stdout, reason)
	case server.ActionCancel:
		// Browser was closed (pagehide sendBeacon, or heartbeat stopped).
		// Degrade to Claude Code's default approval flow so the user isn't
		// stuck watching a hung CLI.
		return hook.WriteAsk(os.Stdout, "plan-reviewer: review window closed, falling back to default approval")
	default:
		return hook.WriteAsk(os.Stdout, "plan-reviewer: unknown action "+result.Action)
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		hook.Logf("unsupported OS %s, please open %s manually", runtime.GOOS, url)
		return
	}
	if err := cmd.Start(); err != nil {
		hook.Logf("failed to open browser: %v", err)
	}
}
