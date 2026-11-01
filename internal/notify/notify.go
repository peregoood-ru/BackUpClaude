// Package notify tells the user about failures. Channels are interchangeable;
// the working ones arrive at stage 5.
package notify

import "context"

const stage = 5

// Message is what a channel delivers.
type Message struct {
	Title string
	Body  string
}

// Channel delivers messages to the user.
type Channel interface {
	Send(ctx context.Context, message Message) error
}
