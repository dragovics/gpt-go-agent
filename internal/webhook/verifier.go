package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var ErrInvalidSignature = errors.New("invalid webhook signature")

// Verify implements the Standard Webhooks signing scheme used by OpenAI.
// The signed content is: webhook-id + "." + webhook-timestamp + "." + raw body.
func Verify(secret, id, timestamp, signature string, body []byte, now time.Time, tolerance time.Duration) error {
	if secret == "" || id == "" || timestamp == "" || signature == "" { return ErrInvalidSignature }
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil { return ErrInvalidSignature }
	if tolerance > 0 && now.Sub(time.Unix(ts, 0)) > tolerance || tolerance > 0 && time.Unix(ts, 0).Sub(now) > tolerance {
		return fmt.Errorf("%w: timestamp outside tolerance", ErrInvalidSignature)
	}
	key := strings.TrimPrefix(secret, "whsec_")
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil { return fmt.Errorf("%w: bad secret", ErrInvalidSignature) }
	mac := hmac.New(sha256.New, decoded)
	_, _ = mac.Write([]byte(id + "." + timestamp + "." + string(body)))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	for _, candidate := range strings.Fields(signature) {
		parts := strings.SplitN(candidate, ",", 2)
		if len(parts) == 2 && hmac.Equal([]byte(want), []byte(parts[1])) {
			return nil
		}
	}
	return ErrInvalidSignature
}
