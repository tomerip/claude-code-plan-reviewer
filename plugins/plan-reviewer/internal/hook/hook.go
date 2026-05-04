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
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
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
