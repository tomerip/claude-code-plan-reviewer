package main

import (
	"embed"
	"encoding/json"
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
		// Return `allow` + `updatedInput` (plus additionalContext) rather than
		// `deny`. Two properties of Claude Code's PreToolUse pipeline combine
		// to give us what we want:
		//
		//   1. `deny` triggers the red "Permission denied by hook" chrome,
		//      which misframes a revision request as a failure.
		//   2. `allow` alone lets ExitPlanMode's native approve/reject dialog
		//      show up because the tool declares requiresUserInteraction().
		//      But if `updatedInput` is present on an allow, the CLI logs
		//      "Hook satisfied user interaction for ... via updatedInput"
		//      and skips the tool's own dialog entirely.
		//
		// ExitPlanMode.call() writes updatedInput.plan back to the plan file
		// itself, so we compute the annotated plan in memory and ship it via
		// updatedInput — without a separate disk write from our side. Two
		// writes (ours + ExitPlanMode's) would bump the plan file's mtime
		// past Claude's cached readFileState timestamp, which would make the
		// subsequent Write during revision fail with "File has been modified
		// since read" / "Error writing file".
		annotated, err := planfile.ApplyFeedback(planPath, result.Comments)
		if err != nil {
			return err
		}
		var toolInput map[string]any
		if len(in.ToolInput) > 0 {
			_ = json.Unmarshal(in.ToolInput, &toolInput)
		}
		if toolInput == nil {
			toolInput = map[string]any{}
		}
		toolInput["plan"] = annotated
		ctx := fmt.Sprintf(
			"The user reviewed this plan and requested revisions rather than approving it outright. "+
				"Inline feedback has been written to %s as `> 💬 FEEDBACK:` blockquotes. "+
				"Re-enter plan mode, re-read that file, address each FEEDBACK annotation, "+
				"remove the FEEDBACK blockquotes, then call ExitPlanMode again with the revised plan.",
			planPath,
		)
		return hook.WriteAllowSatisfiesInteraction(os.Stdout, toolInput, ctx)
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
