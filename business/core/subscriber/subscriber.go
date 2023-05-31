// Package subscriber represents subscriber entities and pool of subscribers.
package subscriber

import (
	"crypto/sha1"
	"encoding/hex"
	"github.com/manmolecular/go-now-here/business/core/message"
)

// bufferMaxMessages defines max size of the message channel for a subscriber.
const bufferMaxMessages = 10

// GenerateSubscriberId generates subscriber identifier as SHA1 hex digest.
func GenerateSubscriberId(content string) string {
	hash := sha1.New()
	hash.Write([]byte(content))
	shaSum := hash.Sum(nil)

	return hex.EncodeToString(shaSum)
}

// Subscriber represents a subscriber.
// Subscriber holds a link with another subscriber which is connected to the current one.
// Messages are sent on the Messages channel and can be represented as message.Message type.
type Subscriber struct {
	Id               string
	PairId           string
	PairDisconnected chan bool
	Messages         chan *message.Message
}

// NewSubscriber constructs a new subscriber using idContent as a content for ID generation
func NewSubscriber(idContent string) *Subscriber {
	return &Subscriber{
		Id:               GenerateSubscriberId(idContent),
		PairDisconnected: make(chan bool, 1),
		Messages:         make(chan *message.Message, bufferMaxMessages),
	}
}
