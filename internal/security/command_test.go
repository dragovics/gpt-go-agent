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
