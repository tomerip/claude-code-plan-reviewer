package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type Input struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	Cwd            string          `json:"cwd"`
	HookEventName  string          `json:"hook_event_name"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
}

type output struct {
	HookSpecificOutput hookSpecific `json:"hookSpecificOutput"`
	SystemMessage      string       `json:"systemMessage,omitempty"`
}

type hookSpecific struct {
	HookEventName            string          `json:"hookEventName"`
	PermissionDecision       string          `json:"permissionDecision"`
	PermissionDecisionReason string          `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string          `json:"additionalContext,omitempty"`
	// Pointer so we can send `{}` (non-nil, empty) when the tool was called
	// with no args — without the pointer, `omitempty` would drop an empty
	// map, and the CLI's bypass check looks for *presence*, not content.
	UpdatedInput *map[string]any `json:"updatedInput,omitempty"`
}

func Read(r io.Reader) (*Input, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	if len(data) == 0 {
		return &Input{}, nil
	}
	in := &Input{}
	if err := json.Unmarshal(data, in); err != nil {
		return nil, fmt.Errorf("parse hook input: %w", err)
	}
	return in, nil
}

func WriteAllow(w io.Writer) error {
	return write(w, output{HookSpecificOutput: hookSpecific{
		HookEventName:      "PreToolUse",
		PermissionDecision: "allow",
	}})
}

func WriteAllowWithContext(w io.Writer, additionalContext string) error {
	return write(w, output{HookSpecificOutput: hookSpecific{
		HookEventName:      "PreToolUse",
		PermissionDecision: "allow",
		AdditionalContext:  additionalContext,
	}})
}

// WriteAllowSatisfiesInteraction bypasses the tool's built-in user-interaction
// dialog (e.g. ExitPlanMode's approve/reject prompt). When `updatedInput` is
// present on an `allow` response AND the tool declares requiresUserInteraction,
// Claude Code treats the hook as having already handled user interaction —
// see the "Hook satisfied user interaction for ... via updatedInput" path in
// the CLI bundle. `updatedInput` must be a valid input object for the target
// tool; passing the original input unchanged is fine and is what we want here.
func WriteAllowSatisfiesInteraction(w io.Writer, toolInput map[string]any, additionalContext string) error {
	if toolInput == nil {
		toolInput = map[string]any{}
	}
	return write(w, output{HookSpecificOutput: hookSpecific{
		HookEventName:      "PreToolUse",
		PermissionDecision: "allow",
		AdditionalContext:  additionalContext,
		UpdatedInput:       &toolInput,
	}})
}

func WriteDeny(w io.Writer, reason string) error {
	return write(w, output{HookSpecificOutput: hookSpecific{
		HookEventName:            "PreToolUse",
		PermissionDecision:       "deny",
		PermissionDecisionReason: reason,
	}})
}

func WriteAsk(w io.Writer, systemMessage string) error {
	return write(w, output{
		HookSpecificOutput: hookSpecific{
			HookEventName:      "PreToolUse",
			PermissionDecision: "ask",
		},
		SystemMessage: systemMessage,
	})
}

func write(w io.Writer, o output) error {
	enc := json.NewEncoder(w)
	return enc.Encode(o)
}

func Logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[plan-reviewer] "+format+"\n", args...)
}
