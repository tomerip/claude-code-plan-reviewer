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
