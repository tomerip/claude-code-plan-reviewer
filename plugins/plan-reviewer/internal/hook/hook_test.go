package hook

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRead_ValidPayload(t *testing.T) {
	raw := `{"session_id":"s1","transcript_path":"/tmp/t.jsonl","cwd":"/tmp","hook_event_name":"PreToolUse","tool_name":"ExitPlanMode","tool_input":{}}`
	in, err := Read(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if in.SessionID != "s1" || in.TranscriptPath != "/tmp/t.jsonl" || in.ToolName != "ExitPlanMode" {
		t.Errorf("parsed wrong: %+v", in)
	}
}

func TestRead_EmptyStdin(t *testing.T) {
	in, err := Read(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if in == nil {
		t.Fatal("expected non-nil input")
	}
	if in.SessionID != "" || in.ToolName != "" {
		t.Errorf("expected zero value, got %+v", in)
	}
}

func TestRead_InvalidJSON(t *testing.T) {
	_, err := Read(strings.NewReader("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestWriteAllow(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteAllow(&buf); err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		HookSpecificOutput struct {
			HookEventName      string `json:"hookEventName"`
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("wrong event name: %+v", parsed)
	}
	if parsed.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("wrong decision: %+v", parsed)
	}
}

func TestWriteDeny(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteDeny(&buf, "please revise"); err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		HookSpecificOutput struct {
			HookEventName            string `json:"hookEventName"`
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("wrong decision: %+v", parsed)
	}
	if parsed.HookSpecificOutput.PermissionDecisionReason != "please revise" {
		t.Errorf("wrong reason: %+v", parsed)
	}
}

func TestWriteAllowSatisfiesInteraction(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteAllowSatisfiesInteraction(&buf, map[string]any{"plan": "p"}, "revise please"); err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		HookSpecificOutput struct {
			HookEventName      string         `json:"hookEventName"`
			PermissionDecision string         `json:"permissionDecision"`
			AdditionalContext  string         `json:"additionalContext"`
			UpdatedInput       map[string]any `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("wrong decision: %+v", parsed)
	}
	if parsed.HookSpecificOutput.AdditionalContext != "revise please" {
		t.Errorf("wrong additionalContext: %+v", parsed)
	}
	if parsed.HookSpecificOutput.UpdatedInput == nil {
		t.Errorf("updatedInput missing — without it the CLI does not bypass requiresUserInteraction")
	}
	if parsed.HookSpecificOutput.UpdatedInput["plan"] != "p" {
		t.Errorf("updatedInput should echo tool input: %+v", parsed.HookSpecificOutput.UpdatedInput)
	}
}

func TestWriteAllowSatisfiesInteraction_NilInput(t *testing.T) {
	// Nil input must still serialize a non-null updatedInput (the CLI's
	// bypass triggers on "!== undefined", not on content).
	var buf bytes.Buffer
	if err := WriteAllowSatisfiesInteraction(&buf, nil, ""); err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		HookSpecificOutput struct {
			UpdatedInput map[string]any `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.HookSpecificOutput.UpdatedInput == nil {
		t.Errorf("updatedInput should be an object, not null/absent, even for nil input")
	}
}

func TestWriteAllowWithContext(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteAllowWithContext(&buf, "re-read and revise"); err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		HookSpecificOutput struct {
			HookEventName      string `json:"hookEventName"`
			PermissionDecision string `json:"permissionDecision"`
			AdditionalContext  string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("wrong decision: %+v", parsed)
	}
	if parsed.HookSpecificOutput.AdditionalContext != "re-read and revise" {
		t.Errorf("wrong additionalContext: %+v", parsed)
	}
}

func TestWriteAsk(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteAsk(&buf, "not sure"); err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.HookSpecificOutput.PermissionDecision != "ask" {
		t.Errorf("wrong decision: %+v", parsed)
	}
	if parsed.SystemMessage != "not sure" {
		t.Errorf("wrong systemMessage: %+v", parsed)
	}
}
