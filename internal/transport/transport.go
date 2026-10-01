package transport

import "context"

type Envelope struct {
	ID      string
	Type    string
	Payload []byte
}
type Transport interface {
	Send(context.Context, Envelope) error
	Receive(context.Context) (Envelope, error)
	Close() error
}
