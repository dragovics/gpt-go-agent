package policy

import "testing"

func TestValidateNativeStrict(t *testing.T) {
	if err := ValidateNativeCommand("uptime", nil, NativeStrict, nil); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNativeCommand("python3", []string{"-c", "print(1)"}, NativeStrict, nil); err == nil {
		t.Fatal("python must be denied in strict mode")
	}
	if err := ValidateNativeCommand("/bin/echo", []string{"x"}, NativeStrict, nil); err == nil {
		t.Fatal("absolute executable must be denied")
	}
}

func TestValidateNativeTrustedRequiresAllowlist(t *testing.T) {
	allowed := map[string]bool{"python3": true}
	if err := ValidateNativeCommand("python3", []string{"-c", "print(1)"}, NativeTrusted, allowed); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNativeCommand("node", nil, NativeTrusted, allowed); err == nil {
		t.Fatal("non-allowlisted command must be denied")
	}
}

func TestParseNativeMode(t *testing.T) {
	if got, err := ParseNativeMode(""); err != nil || got != NativeStrict {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if got, err := ParseNativeMode("trusted"); err != nil || got != NativeTrusted {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if _, err := ParseNativeMode("unsafe-ish"); err == nil {
		t.Fatal("expected invalid mode")
	}
}
