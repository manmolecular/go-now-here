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

// bufferMaxMessages defines max size of the message channel for a subscriber
const bufferMaxMessages = 10

// publishLimiterMilliseconds defines rate limit for a message publishing
const publishLimiterMilliseconds = 100

// publishLimiterBurst allows burst of messages of size N
const publishLimiterBurst = 10

// checkSubAvailabilitySeconds defines rate of available subscribers checking interval
const checkSubAvailabilitySeconds = 1

// checkSubAvailabilityMaxTimeSeconds defines how long can we wait for an available subscriber
const checkSubAvailabilityMaxTimeSeconds = 5

// subscriber represents a subscriber.
// Messages are sent on the messages channel and can be represented as any object.
type subscriber struct {
	id       string
	pairId   string
	messages chan any
}

// Handlers manages the set of chat endpoints.
type Handlers struct {
	log *zap.SugaredLogger

	// subscriberMessageBuffer controls the max number of messages that can be queued for a subscriber.
	subscriberMessageBuffer int

	subscribersMu sync.Mutex
	subscribers   map[string]*subscriber

	// publishLimiter controls the rate limit applied to the publishMessage function.
	publishLimiter *rate.Limiter
}

// New constructs a Handlers api for the chat group.
func New(log *zap.SugaredLogger) *Handlers {
	return &Handlers{
		log:                     log,
		subscriberMessageBuffer: bufferMaxMessages,
		subscribers:             make(map[string]*subscriber),
		publishLimiter:          rate.NewLimiter(rate.Every(publishLimiterMilliseconds*time.Millisecond), publishLimiterBurst),
	}
}

// publishMessage publishes a message to the message channel.
func (h *Handlers) publishMessage(msg any, s *subscriber) {
	h.subscribersMu.Lock()
	defer h.subscribersMu.Unlock()

	if err := h.publishLimiter.Wait(context.Background()); err != nil {
		h.log.Warnw("msg can not be published", "subscriberId", s.id, "error", err)
	}

	// Message should be published to the paired subscriber only
	h.subscribers[s.pairId].messages <- msg
}

// assignSubscriberPair searches for an available pair for a new subscriber.
func (h *Handlers) assignSubscriberPair(ctx context.Context, s *subscriber) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ticker := time.NewTicker(checkSubAvailabilitySeconds)
	defer ticker.Stop()

	isFound := make(chan bool, 1)

	go func() {
		for range ticker.C {
			// If subscriber already received a pair - success, chat is possible
			if s.pairId != "" {
				h.log.Debugw("pair for a subscriber is found", "subscriberId", s.id, "pairId", s.pairId, "status", "success")
				isFound <- true
				return
			}

			select {
			case <-ctx.Done():
				h.log.Debugw("pair search is finished (found or timed out)", "subscriberId", s.id, "reason", "foundOrTimeout")
				return
			default:
				h.log.Debugw("searching for a pair to assign for a subscriber", "subscriberId", s.id, "status", "inProgress")

				for idAnother, sAnother := range h.subscribers {
					// If another pair is assigned or another pair is the current subscriber - continue search
					if sAnother.pairId != "" || idAnother == s.id {
						continue
					}

					// Available pair is found, assign it
					h.subscribersMu.Lock()
					h.subscribers[idAnother].pairId = s.id
					h.subscribers[s.id].pairId = idAnother
					h.subscribersMu.Unlock()
					h.log.Debugw("pairs for subscribers are assigned",
						"subscriberId", h.subscribers[s.id].id,
						"subscriberPairId", h.subscribers[s.id].pairId,
						"anotherId", h.subscribers[idAnother].id,
						"anotherPairId", h.subscribers[idAnother].pairId,
						"status", "success")
					isFound <- true
				}
			}
		}
	}()

	select {
	case <-isFound:
		h.log.Debugw("pair is found", "subscriberId", s.id, "status", "success")
		return nil
	case <-time.After(checkSubAvailabilityMaxTimeSeconds * time.Second):
		h.log.Debugw("pair was not found, time out reached", "subscriberId", s.id, "status", "failure")
		return errors.New("pair search timeout")
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

// readMessages reads messages from a client and publish them to the message channel when available.
func (h *Handlers) readMessages(ctx context.Context, conn *websocket.Conn, s *subscriber) context.Context {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		for {
			select {
			case <-ctx.Done():
				h.log.Debugw("read context is done", "subscriberId", s.id)
				return
			default:
				var message interface{}
				if err := wsjson.Read(ctx, conn, &message); err != nil {
					if errors.As(err, &websocket.CloseError{}) {
						h.log.Warnw("client closed the connection", "error", err, "subscriberId", s.id)
					} else {
						h.log.Errorw("unexpected error while reading a message", "error", err, "subscriberId", s.id)
					}

					// TODO: handle other possible types of websocket errors

					cancel()
					continue
				}

				h.log.Debugw("read message from a subscriber", "message", message, "subscriberId", s.id)
				h.publishMessage(message, s)
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
				h.log.Debugw("write message to a subscriber", "message", message, "subscriberId", s.id)
				if err := wsjson.Write(ctx, conn, message); err != nil {
					h.log.Warnw("no new messages to write", "error", err, "subscriberId", s.id)
				}
			case <-ctx.Done():
				h.log.Debugw("write context is done", "subscriberId", s.id)
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
		h.log.Debugw("no subscriber pair is available", "subscriberId", s.id, "error", err)
		if err = conn.Close(websocket.StatusTryAgainLater, "no available subscribers"); err != nil {
			h.log.Warnw("socket connection can not be closed", "error", err, "subscriberId", s.id)
		}

		return nil
	}

	// Read messages in a goroutine, ctx will be canceled if client closed the connection
	ctx = h.readMessages(ctx, conn, s)

	// Write messages to a client from the message channel in goroutine
	h.writeMessages(ctx, conn, s)

	// Block handler while reader and writer goroutines are performing operations
	select {}
}
