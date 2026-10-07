package security

import "testing"

func TestValidateRestrictedCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		wantErr bool
	}{
		{name: "echo", command: "echo", args: []string{"ok"}},
		{name: "uptime", command: "uptime"},
		{name: "python denied", command: "python3", args: []string{"-c", "print(1)"}, wantErr: true},
		{name: "shell denied", command: "sh", args: []string{"-c", "id"}, wantErr: true},
		{name: "absolute denied", command: "/bin/echo", wantErr: true},
		{name: "ls args denied", command: "ls", args: []string{"/etc"}, wantErr: true},
		{name: "ls traversal denied", command: "ls", args: []string{"../parent"}, wantErr: true},
		{name: "ls safe flags allowed", command: "ls", args: []string{"-la", "subdir"}},
		{name: "git status allowed", command: "git", args: []string{"status", "-sb"}},
		{name: "git diff allowed", command: "git", args: []string{"diff", "--stat"}},
		{name: "git commit denied", command: "git", args: []string{"commit", "-m", "bad"}, wantErr: true},
		{name: "git push denied", command: "git", args: []string{"push"}, wantErr: true},
		{name: "git flag exec denied", command: "git", args: []string{"status", "--exec=sh"}, wantErr: true},
		{name: "grep safe allowed", command: "grep", args: []string{"-rn", "func", "."}},
		{name: "grep escape denied", command: "grep", args: []string{"-rn", "func", "/etc"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRestrictedCommand(tt.command, tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}
