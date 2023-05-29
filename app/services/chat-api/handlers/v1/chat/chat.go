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
	id       string
	pairId   string
	messages chan any
}

// Handlers manages the set of chat endpoints.
type Handlers struct {
	Log *zap.SugaredLogger

	// subscriberMessageBuffer controls the max number of messages that can be queued for a subscriber.
	subscriberMessageBuffer int

	subscribersMu sync.Mutex
	subscribers   map[string]*subscriber

	// publishLimiter controls the rate limit applied to the publish function.
	publishLimiter *rate.Limiter
}

// New constructs a Handlers api for the chat group.
func New(log *zap.SugaredLogger) *Handlers {
	return &Handlers{
		Log:                     log,
		subscriberMessageBuffer: BufferMaxMessages,
		subscribers:             make(map[string]*subscriber),
		publishLimiter:          rate.NewLimiter(rate.Every(PublishLimiterMilliseconds*time.Millisecond), PublishLimiterBurst),
	}
}

// publish publishes a message to the message channel.
func (h *Handlers) publish(message any, s *subscriber) {
	h.subscribersMu.Lock()
	defer h.subscribersMu.Unlock()

	if err := h.publishLimiter.Wait(context.Background()); err != nil {
		h.Log.Warnf("message can not be published, details: %v", err)
	}

	// Message should be published to the exact paired subscriber
	h.subscribers[s.pairId].messages <- message
}

// assignSubscriberPair looks for a matching pair to a new subscriber every second
func (h *Handlers) assignSubscriberPair(ctx context.Context, s *subscriber) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	isFound := make(chan bool, 1)

	go func() {
		h.Log.Infof("searching for a pair to assign for a subscriber: %s", s.id)

		for range time.Tick(1 * time.Second) {
			select {
			default:
				// If subscriber already has an assigned pair - condition is met, chat is possible
				if h.subscribers[s.id].pairId != "" {
					h.Log.Infof("success, pair is found: %s", h.subscribers[s.id].pairId)
					isFound <- true
				}

				for idAnother, sAnother := range h.subscribers {
					// If another pair is not available or another pair is the current subscriber - continue search
					if sAnother.pairId != "" || idAnother == s.id {
						continue
					}
					h.subscribersMu.Lock()
					h.subscribers[idAnother].pairId = s.id
					h.subscribers[s.id].pairId = idAnother
					h.subscribersMu.Unlock()
					h.Log.Infof(
						"success, pairs are assigned:(%s, %s) <-> (%s, %s)",
						h.subscribers[idAnother].id, h.subscribers[idAnother].pairId,
						h.subscribers[s.id].id, h.subscribers[s.id].pairId,
					)
					isFound <- true
				}
			case <-ctx.Done():
				h.Log.Info("pair searching is finished")
				return
			}
		}
	}()

	select {
	case <-isFound:
		h.Log.Info("success, stop pair searching")
		return nil
	case <-time.After(5 * time.Second):
		h.Log.Info("pair was not found, time out reached")
		return errors.New("pair searching timeout")
	}
}

// addSubscriber registers a subscriber.
func (h *Handlers) addSubscriber(s *subscriber) {
	h.subscribersMu.Lock()
	h.subscribers[s.id] = s
	h.subscribersMu.Unlock()
}

// deleteSubscriber deletes the given subscriber.
func (h *Handlers) deleteSubscriber(s *subscriber) {
	h.subscribersMu.Lock()
	delete(h.subscribers, s.id)
	h.subscribersMu.Unlock()
}

// readMessages waits messages from a client and publish them to the message channel when available.
func (h *Handlers) readMessages(ctx context.Context, conn *websocket.Conn, s *subscriber) context.Context {
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
				h.publish(message, s)
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
		id:       r.RemoteAddr,
		messages: make(chan any, h.subscriberMessageBuffer),
	}

	h.addSubscriber(s)
	defer h.deleteSubscriber(s)

	// Assign a pair to chat with.
	// When pair is successfully assigned, we can read messages from the pair and write messages to the pair
	if err = h.assignSubscriberPair(ctx, s); err != nil {
		h.Log.Warn("no subscriber pair is available")
		conn.Close(websocket.StatusTryAgainLater, "no available subscribers")
		return nil
	}

	// Read messages in a goroutine, ctx will be canceled if client closed the connection
	ctx = h.readMessages(ctx, conn, s)

	// Write messages to a client from the message channel in goroutine
	h.writeMessages(ctx, conn, s)

	// Block handler while reader and writer goroutines are performing operations
	select {}
}
