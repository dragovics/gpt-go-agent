package policy

import "testing"

func TestPolicyDeniesWritesByDefault(t *testing.T) {
	p := Policy{}
	if d := p.Check("write_file", "/tmp/x", true, false); d.Allowed {
		t.Fatal("expected write to be denied")
	}
}

func TestPolicyAllowsRead(t *testing.T) {
	p := Policy{}
	if d := p.Check("read_file", "/tmp/x", false, false); !d.Allowed {
		t.Fatalf("read denied: %s", d.Reason)
	}
}
