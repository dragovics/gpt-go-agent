package webhook

import (
	"context"
	"errors"
	"testing"
)

func TestRetryLoopExponential(t *testing.T) {
	attempts := 0
	err := RetryLoop(context.Background(), func() error {
		attempts++
		return errors.New("no")
	}, 4, 0)
	if err == nil || attempts != 4 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
}

func TestRetryLoopStopsOnSuccess(t *testing.T) {
	attempts := 0
	err := RetryLoop(context.Background(), func() error {
		attempts++
		if attempts == 2 {
			return nil
		}
		return errors.New("retry")
	}, 4, 0)
	if err != nil || attempts != 2 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
}
