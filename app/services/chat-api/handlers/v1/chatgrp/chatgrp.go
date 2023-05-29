// Package chatgrp chat maintains the group of handlers for chat handling via websockets.
package chatgrp

import (
	"context"
	"errors"
	"fmt"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"net/http"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
	"sync"
	"time"
)

const (
	// bufferMaxMessages defines max size of the message channel for a subscriber
	bufferMaxMessages = 10

	// publishLimiterBurst allows burst of messages of size N
	publishLimiterBurst = 10

	// publishLimiterMilliseconds defines rate limit for a message publishing
	publishLimiterMilliseconds = 100

	// checkSubAvailabilityMilliseconds defines rate of available subscribers checking interval
	checkSubAvailabilityMilliseconds = 1_000

	// checkSubAvailabilityMaxTimeSeconds defines how long can we wait for an available subscriber
	checkSubAvailabilityMaxTimeSeconds = 5
)

// subscriber represents a subscriber.
// Messages are sent on the messages channel and can be represented as any object.
type subscriber struct {
	id       string
	pairId   string
	messages chan any
}

// message represents a message.
type message struct {
	Content string `json:"content"`
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
	limiter := rate.NewLimiter(rate.Every(publishLimiterMilliseconds*time.Millisecond), publishLimiterBurst)
	return &Handlers{
		log:                     log,
		subscriberMessageBuffer: bufferMaxMessages,
		subscribers:             make(map[string]*subscriber),
		publishLimiter:          limiter,
	}
}

// publishMessage publishes a message to the message channel of a paired subscriber.
func (h *Handlers) publishMessage(msg any, s *subscriber) error {
	h.subscribersMu.Lock()
	defer h.subscribersMu.Unlock()

	if err := h.publishLimiter.Wait(context.Background()); err != nil {
		return fmt.Errorf("message %s can not be published, limiter error: %w", msg, err)
	}

	// Message must be published to the paired subscriber only
	sAnother, ok := h.subscribers[s.pairId]
	if !ok {
		return fmt.Errorf("message %s can not be published, no available pair to send message", msg)
	}

	sAnother.messages <- msg
	h.log.Debugw("message sent successfully", "subscriberId", s.id, "anotherId", sAnother.id)

	return nil
}

// linkSubscribersById links two available subscribers with each other as a pair.
func (h *Handlers) linkSubscribersById(sId, anotherId string) {
	h.subscribersMu.Lock()
	defer h.subscribersMu.Unlock()

	h.subscribers[anotherId].pairId = sId
	h.subscribers[sId].pairId = anotherId

	h.log.Debugw("successfully linkeds two subscribers in a pair",
		"subscriberId", h.subscribers[sId].id,
		"subscriberPairId", h.subscribers[sId].pairId,
		"anotherId", h.subscribers[anotherId].id,
		"anotherPairId", h.subscribers[anotherId].pairId,
		"status", "success")
}

// assignAvailableSubscriber assigns an available (if any) subscriber for a new subscriber.
func (h *Handlers) assignAvailableSubscriber(ctx context.Context, s *subscriber) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ticker := time.NewTicker(checkSubAvailabilityMilliseconds * time.Millisecond)
	defer ticker.Stop()

	isFound := make(chan bool, 1)

	go func() {
		for range ticker.C {
			// If the current subscriber already has a pair - success, pair exists
			if s.pairId != "" {
				h.log.Debugw("pair for a subscriber exists", "subscriberId", s.id, "pairId", s.pairId, "status", "success")
				isFound <- true
				return
			}

			select {
			case <-ctx.Done():
				h.log.Debugw("pair search is finished (found or timed out)", "subscriberId", s.id, "reason", "foundOrTimeout")
				return
			default:
				h.log.Debugw("searching for an available pair to assign for a subscriber", "subscriberId", s.id, "status", "inProgress")
				for idAnother, sAnother := range h.subscribers {
					// If another subscriber already has a pair (not available) or it is the current subscriber - skip
					if sAnother.pairId != "" || idAnother == s.id {
						continue
					}

					// Otherwise, the current subscriber and another one are both available to be linked
					h.linkSubscribersById(s.id, sAnother.id)
					isFound <- true
				}
			}
		}
	}()

	select {
	case <-isFound:
		h.log.Debugw("pair was found", "subscriberId", s.id, "status", "success")
		return nil
	case <-time.After(checkSubAvailabilityMaxTimeSeconds * time.Second):
		h.log.Debugw("pair search time out reached", "subscriberId", s.id, "status", "failure")
		return errors.New("pair search time out reached")
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
	defer h.subscribersMu.Unlock()

	// If the current subscriber has a pair, make the pair available for a new subscriber (since the current is not
	// active anymore). Otherwise, subscriber was not able to find a pair, so nothing to delete here
	sAnother, ok := h.subscribers[s.pairId]
	if ok {
		sAnother.pairId = ""
		h.log.Debugw("pair subscriber is free now", "subscriberId", s.id, "subscriberPairId", s.pairId)
	}

	delete(h.subscribers, s.id)
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
				var msg interface{}
				if err := wsjson.Read(ctx, conn, &msg); err != nil {
					if errors.As(err, &websocket.CloseError{}) {
						h.log.Warnw("message can not be read, client closed the connection", "subscriberId", s.id)
						cancel()
						continue
					}

					h.log.Warnw("message can not be read due to error", "subscriberId", s.id, "error", err)
					// Skip this message, try to take the next one
					continue
				}

				h.log.Debugw("read message from a subscriber", "subscriberId", s.id, "message", msg)

				if err := h.publishMessage(msg, s); err != nil {
					h.log.Warnw("message can not be published due to error", "subscriberId", s.id, "error", err)
					// Skip this message, try to take the next one
					continue
				}
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
			case msg := <-s.messages:
				h.log.Debugw("write message to a subscriber", "subscriberId", s.id, "message", msg)
				if err := wsjson.Write(ctx, conn, msg); err != nil {
					h.log.Warnw("message can not be written due to error", "subscriberId", s.id, "error", err)
					// Skip this message, try to take the next one
					continue
				}
			case <-ctx.Done():
				h.log.Debugw("write context is done", "subscriberId", s.id)
				return
			}
		}
	}()
}

// notifySubscriber notifies subscriber about any service-related actions
func (h *Handlers) notifySubscriber(ctx context.Context, conn *websocket.Conn, sId string, msg string) {
	if err := wsjson.Write(ctx, conn, &message{Content: msg}); err != nil {
		h.log.Warnw("subscriber can not be notified", "error", err, "subscriberId", sId)
	}
}

// Subscribe subscribes a client to the message channel to read and write messages from it
func (h *Handlers) Subscribe(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	// Accept connection from the client
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return err
	}

	defer conn.Close(websocket.StatusNormalClosure, "")

	s := &subscriber{
		id:       r.RemoteAddr,
		messages: make(chan any, h.subscriberMessageBuffer),
	}

	h.addSubscriber(s)
	defer h.deleteSubscriber(s)

	h.notifySubscriber(ctx, conn, s.id, "wait for any available client to chat with...")

	// Assign a pair to chat with.
	// When pair is successfully assigned, we can read messages from the pair and write messages to the pair
	if err = h.assignAvailableSubscriber(ctx, s); err != nil {
		h.log.Debugw("no subscriber pair is available", "subscriberId", s.id, "error", err)
		h.notifySubscriber(ctx, conn, s.id, "no available clients to chat with; please, reload the page and try again later.")

		if err = conn.Close(websocket.StatusTryAgainLater, "no available subscribers"); err != nil {
			h.log.Warnw("socket connection can not be closed", "error", err, "subscriberId", s.id)
		}

		return nil
	}

	h.notifySubscriber(ctx, conn, s.id, "available client is found.")

	// Read messages in a goroutine, ctx will be canceled if client closed the connection
	ctx = h.readMessages(ctx, conn, s)

	// Write messages to a client from the message channel in goroutine
	h.writeMessages(ctx, conn, s)

	// Block handler while reader and writer goroutines are performing operations
	select {
	case <-ctx.Done():
		h.log.Debugw("client closed the connection", "subscriberId", s.id)
	}

	return nil
}
