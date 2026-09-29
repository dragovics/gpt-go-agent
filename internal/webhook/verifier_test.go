package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"
)

func TestVerify(t *testing.T) {
	raw := []byte("{"type":"agent.session.action_required"}")
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-secret"))
	id := "evt_123"
	ts := time.Now().Unix()
	timestamp := time.Unix(ts, 0)
	m := hmac.New(sha256.New, []byte("test-secret"))
	_, _ = m.Write([]byte(id + "." + timestamp.Format("2006") + "." + string(raw)))
	// Rebuild using the exact Unix timestamp string used by the protocol.
	m = hmac.New(sha256.New, []byte("test-secret"))
	_, _ = m.Write([]byte(id + "." + fmt.Sprint(ts) + "." + string(raw)))
	sig := "v1," + base64.StdEncoding.EncodeToString(m.Sum(nil))
	if err := Verify(secret, id, fmt.Sprint(ts), sig, raw, timestamp, time.Minute); err != nil {
		t.Fatal(err)
	}
}
