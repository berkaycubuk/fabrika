package agent

import "testing"

func TestStreamCommand(t *testing.T) {
	const injected = " --output-format stream-json --verbose"

	tests := []struct {
		name       string
		command    string
		wantCmd    string
		wantStream bool
	}{
		{
			name:       "empty",
			command:    "",
			wantCmd:    "",
			wantStream: false,
		},
		{
			name:       "whitespace only",
			command:    "   \t\n ",
			wantCmd:    "",
			wantStream: false,
		},
		{
			name:       "claude bare",
			command:    "claude -p 'do the task'",
			wantCmd:    "claude -p 'do the task'" + injected,
			wantStream: true,
		},
		{
			name:       "claude with absolute path",
			command:    "/usr/local/bin/claude -p hi",
			wantCmd:    "/usr/local/bin/claude -p hi" + injected,
			wantStream: true,
		},
		{
			name:       "already stream-json is idempotent",
			command:    "claude -p hi --output-format stream-json --verbose",
			wantCmd:    "claude -p hi --output-format stream-json --verbose",
			wantStream: true,
		},
		{
			name:       "stream-json elsewhere in command",
			command:    "claude --output-format=stream-json -p hi",
			wantCmd:    "claude --output-format=stream-json -p hi",
			wantStream: true,
		},
		{
			name:       "claude mentioned not as program",
			command:    "env CLI=claude run -p hi",
			wantCmd:    "env CLI=claude run -p hi" + injected,
			wantStream: true,
		},
		{
			name:       "opencode gets --format json injected",
			command:    "opencode run -p hi",
			wantCmd:    "opencode run -p hi --format json",
			wantStream: true,
		},
		{
			name:       "opencode with absolute path",
			command:    "/usr/local/bin/opencode run -p hi",
			wantCmd:    "/usr/local/bin/opencode run -p hi --format json",
			wantStream: true,
		},
		{
			name:       "opencode --format json idempotent",
			command:    "opencode run -p hi --format json",
			wantCmd:    "opencode run -p hi --format json",
			wantStream: true,
		},
		{
			name:       "opencode --format=json idempotent",
			command:    "opencode run -p hi --format=json",
			wantCmd:    "opencode run -p hi --format=json",
			wantStream: true,
		},
		{
			name:       "opencode pinned non-json --format stays buffered",
			command:    "opencode run -p hi --format text",
			wantCmd:    "opencode run -p hi --format text",
			wantStream: false,
		},
		{
			name:       "other CLI unchanged",
			command:    "/usr/bin/some-agent --do",
			wantCmd:    "/usr/bin/some-agent --do",
			wantStream: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCmd, gotStream := StreamCommand(tt.command)
			if gotCmd != tt.wantCmd {
				t.Errorf("StreamCommand(%q) cmd = %q, want %q", tt.command, gotCmd, tt.wantCmd)
			}
			if gotStream != tt.wantStream {
				t.Errorf("StreamCommand(%q) stream = %v, want %v", tt.command, gotStream, tt.wantStream)
			}
		})
	}
}
