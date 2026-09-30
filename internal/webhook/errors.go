package webhook

import "errors"

var (
	ErrInvalidInput        = errors.New("invalid webhook input")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
	ErrJobCapacity         = errors.New("webhook job capacity reached")
	ErrQueueFull           = errors.New("webhook worker queue is full")
)
