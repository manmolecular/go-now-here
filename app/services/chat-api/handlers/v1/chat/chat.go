// Package chat maintains the group of handlers for chat handling via websockets.
package chat

import (
	"context"
	"errors"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"net/http"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
	"sync"
	"time"
)

const BufferMaxMessages = 10
const PublishLimiterMilliseconds = 100
const PublishLimiterBurst = 10

// subscriber represents a subscriber.
// Messages are sent on the messages channel and can be represented as any object.
type subscriber struct {
	messages chan any
}

// Handlers manages the set of chat endpoints.
type Handlers struct {
	Log *zap.SugaredLogger

	// subscriberMessageBuffer controls the max number of messages that can be queued for a subscriber.
	subscriberMessageBuffer int

	subscribersMu sync.Mutex
	subscribers   map[*subscriber]struct{}

	// publishLimiter controls the rate limit applied to the publish function.
	publishLimiter *rate.Limiter
}

// New constructs a Handlers api for the chat group.
func New(log *zap.SugaredLogger) *Handlers {
	return &Handlers{
		Log:                     log,
		subscriberMessageBuffer: BufferMaxMessages,
		subscribers:             make(map[*subscriber]struct{}),
		publishLimiter:          rate.NewLimiter(rate.Every(PublishLimiterMilliseconds*time.Millisecond), PublishLimiterBurst),
	}
}

// publish publishes a message to the message channel.
func (h *Handlers) publish(message any) {
	h.subscribersMu.Lock()
	defer h.subscribersMu.Unlock()

	if err := h.publishLimiter.Wait(context.Background()); err != nil {
		h.Log.Warnf("message can not be published, detais: %v", err)
	}

	for s := range h.subscribers {
		select {
		case s.messages <- message:
			h.Log.Infof("published message to the channel: %v", message)
		}
	}
}

// addSubscriber registers a subscriber.
func (h *Handlers) addSubscriber(s *subscriber) {
	h.subscribersMu.Lock()
	h.subscribers[s] = struct{}{}
	h.subscribersMu.Unlock()
}

// deleteSubscriber deletes the given subscriber.
func (h *Handlers) deleteSubscriber(s *subscriber) {
	h.subscribersMu.Lock()
	delete(h.subscribers, s)
	h.subscribersMu.Unlock()
}

// readMessages waits messages from a client and publish them to the message channel when available.
func (h *Handlers) readMessages(ctx context.Context, conn *websocket.Conn) context.Context {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		for {
			select {
			case <-ctx.Done():
				h.Log.Warnf("read context is done")
				return
			default:
				var message interface{}
				if err := wsjson.Read(ctx, conn, &message); err != nil {
					if errors.As(err, &websocket.CloseError{}) {
						h.Log.Warnf("client closed the connection, details: %v", err)
					} else {
						h.Log.Errorf("unexpected error while reading a message: %v", err)
					}

					// TODO: handle other possible types of websocket errors

					cancel()
					continue
				}

				h.Log.Infof("read message from a client: %v", message)
				h.publish(message)
			}
		}
	}()

	return ctx
}

// writeMessages writes messages from the message channel to the client
func (h *Handlers) writeMessages(ctx context.Context, conn *websocket.Conn, s *subscriber) {
	go func() {
		for {
			select {
			case message := <-s.messages:
				h.Log.Infof("write message to a client: %v", message)
				if err := wsjson.Write(ctx, conn, message); err != nil {
					h.Log.Errorf("no new messages to write: %v", err)
				}
			case <-ctx.Done():
				h.Log.Warnf("write context is done")
				return
			}
		}
	}()
}

// Subscribe subscribes a client to the message channel to read and write messages from it
func (h *Handlers) Subscribe(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	// Accept connection from the client
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return err
	}

	s := &subscriber{
		messages: make(chan any, h.subscriberMessageBuffer),
	}

	h.addSubscriber(s)
	defer h.deleteSubscriber(s)

	// Read messages in a goroutine, ctx will be canceled if client closed the connection
	ctx = h.readMessages(ctx, conn)

	// Write messages to a client from the message channel in goroutine
	h.writeMessages(ctx, conn, s)

	// Block handler while reader and writer goroutines are performing operations
	select {}
}
