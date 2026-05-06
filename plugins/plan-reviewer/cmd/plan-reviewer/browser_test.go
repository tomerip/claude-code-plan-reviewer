package main

import "testing"

func TestDetectOpenMode(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want openMode
	}{
		{
			name: "no ssh, no vscode -> local",
			env:  map[string]string{},
			want: modeLocal,
		},
		{
			name: "vscode locally (not ssh) -> local, VS Code terminal opens localhost natively",
			env:  map[string]string{"TERM_PROGRAM": "vscode", "VSCODE_INJECTION": "1"},
			want: modeLocal,
		},
		{
			name: "ssh via SSH_CONNECTION, no vscode -> ssh hint",
			env:  map[string]string{"SSH_CONNECTION": "1.2.3.4 55555 5.6.7.8 22"},
			want: modeSSHNoIDE,
		},
		{
			name: "ssh via SSH_TTY only -> ssh hint",
			env:  map[string]string{"SSH_TTY": "/dev/pts/0"},
			want: modeSSHNoIDE,
		},
		{
			name: "ssh via SSH_CLIENT only -> ssh hint",
			env:  map[string]string{"SSH_CLIENT": "1.2.3.4 55555 22"},
			want: modeSSHNoIDE,
		},
		{
			name: "ssh + vscode via TERM_PROGRAM -> vscode",
			env:  map[string]string{"SSH_CONNECTION": "x", "TERM_PROGRAM": "vscode"},
			want: modeVSCode,
		},
		{
			name: "ssh + vscode via VSCODE_INJECTION -> vscode",
			env:  map[string]string{"SSH_CONNECTION": "x", "VSCODE_INJECTION": "1"},
			want: modeVSCode,
		},
		{
			name: "ssh + vscode via VSCODE_IPC_HOOK_CLI -> vscode",
			env:  map[string]string{"SSH_CONNECTION": "x", "VSCODE_IPC_HOOK_CLI": "/tmp/x.sock"},
			want: modeVSCode,
		},
		{
			name: "ssh with TERM_PROGRAM=xterm (not vscode) -> ssh hint",
			env:  map[string]string{"SSH_CONNECTION": "x", "TERM_PROGRAM": "xterm-256color"},
			want: modeSSHNoIDE,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			if got := detectOpenMode(getenv); got != tc.want {
				t.Errorf("detectOpenMode() = %v, want %v", got, tc.want)
			}
		})
	}
}
