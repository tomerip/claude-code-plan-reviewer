package main

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"plan-reviewer/internal/hook"
	"plan-reviewer/internal/planfile"
	"plan-reviewer/internal/server"
	"plan-reviewer/internal/theme"
)

//go:embed web/*
var assetsFS embed.FS

const reviewTimeout = 2 * time.Hour

func main() {
	// Support `--help` without a hook payload.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--help", "-h":
			fmt.Println("plan-reviewer — internal binary for the plan-reviewer Claude Code plugin.")
			fmt.Println("Reads a PreToolUse hook payload on stdin and emits a permission decision on stdout.")
			return
		case "--version":
			fmt.Println("plan-reviewer 0.3.1")
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
		// Build updatedInput from scratch with only `plan` — never forward
		// other keys from in.ToolInput. ToolInput is ultimately
		// model-generated and could be influenced by prompt injection of
		// the transcript; ExitPlanMode only consumes `plan`, so dropping
		// everything else keeps us from coupling our safety to internal
		// CC tool behavior.
		toolInput := map[string]any{"plan": annotated}
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

// openMode picks how to surface the review URL to the user.
//
//   - modeLocal: ordinary desktop session — hand the URL to the OS's default
//     browser via `open` / `xdg-open`.
//   - modeVSCode: running inside a VS Code integrated terminal (most often
//     via Remote-SSH). VS Code sets $BROWSER to a shim that opens the URL in
//     the user's local browser and auto-forwards the 127.0.0.1 port through
//     the existing SSH tunnel, so no user action is needed.
//   - modeSSHNoIDE: plain SSH without VS Code. We can't reach back to the
//     client, so we print the URL plus a port-forward hint and let the user
//     set up their own tunnel.
type openMode int

const (
	modeLocal openMode = iota
	modeVSCode
	modeSSHNoIDE
)

func detectOpenMode(getenv func(string) string) openMode {
	if !isSSHSession(getenv) {
		return modeLocal
	}
	if isVSCodeShell(getenv) {
		return modeVSCode
	}
	return modeSSHNoIDE
}

func isSSHSession(getenv func(string) string) bool {
	return getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" || getenv("SSH_CLIENT") != ""
}

// isVSCodeShell recognizes the VS Code integrated terminal (local or Remote-SSH)
// via env vars VS Code injects. $VSCODE_INJECTION is the only officially
// documented signal; the others are de-facto reliable but undocumented, so we
// check all three to be robust to VS Code internals shifting.
func isVSCodeShell(getenv func(string) string) bool {
	return getenv("VSCODE_INJECTION") != "" ||
		getenv("VSCODE_IPC_HOOK_CLI") != "" ||
		getenv("TERM_PROGRAM") == "vscode"
}

func openBrowser(url string) {
	switch detectOpenMode(os.Getenv) {
	case modeVSCode:
		openBrowserVSCode(url)
	case modeSSHNoIDE:
		printSSHForwardHint(url)
	default:
		openBrowserLocal(url)
	}
}

func openBrowserLocal(url string) {
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

// openBrowserVSCode tries $BROWSER first (VS Code sets this to a shim that
// opens the URL in the local browser + auto-forwards the port). If $BROWSER
// is unset or its invocation fails, fall back to `code --open-url`, which is
// undocumented but observed to route URLs through the same local-open path.
func openBrowserVSCode(url string) {
	if browser := os.Getenv("BROWSER"); browser != "" {
		if parts := strings.Fields(browser); len(parts) > 0 {
			args := append(parts[1:], url)
			err := exec.Command(parts[0], args...).Start()
			if err == nil {
				return
			}
			hook.Logf("$BROWSER failed (%v); trying `code --open-url`", err)
		}
	}
	if _, err := exec.LookPath("code"); err == nil {
		if err := exec.Command("code", "--open-url", url).Start(); err == nil {
			return
		} else {
			hook.Logf("`code --open-url` failed: %v", err)
		}
	}
	// Last resort: print the URL + forward hint. VS Code's auto-port-forward
	// kicks in when the user clicks a localhost URL in the integrated terminal.
	printSSHForwardHint(url)
}

// printSSHForwardHint writes the URL and an SSH -L forward hint somewhere
// the user can see it. Goes to stderr (which Claude Code surfaces as hook
// output) and to /dev/tty directly (belt-and-braces — guaranteed visibility
// even if the CLI swallows stderr).
func printSSHForwardHint(url string) {
	port := ""
	if i := strings.LastIndex(url, ":"); i >= 0 {
		port = url[i+1:]
	}
	msg := fmt.Sprintf(
		"plan-reviewer: running over SSH — open the review UI by forwarding port %s:\n"+
			"  ssh -O forward -L %s:127.0.0.1:%s <your-ssh-host>   # if ControlMaster is on\n"+
			"  or add to ~/.ssh/config:  LocalForward %s 127.0.0.1:%s\n"+
			"Then open %s in your local browser.",
		port, port, port, port, port, url,
	)
	hook.Logf("%s", msg)
	if f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		fmt.Fprintln(f, msg)
		_ = f.Close()
	}
}
