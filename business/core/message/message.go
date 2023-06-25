// Package message represents message that can be sent over subscribers.
package message

// Message represents a message.
type Message struct {
	Content string `json:"content"`
	From    string `json:"from"`
	Status  string `json:"status"`
}

// NewMessage constructs a new message using text content, sender and message status (if any).
func NewMessage(content, from, status string) *Message {
	return &Message{
		Content: content,
		From:    from,
		Status:  status,
	}
}
