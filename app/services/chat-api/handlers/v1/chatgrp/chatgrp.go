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
	searchPairTimeoutError = "pair search time out reached: pair can not be found"
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

// message represents a message.
type message struct {
	Content string `json:"content"`
}

// subscriber represents a subscriber.
// Messages are sent on the messages channel and can be represented as message type.
type subscriber struct {
	id       string
	pairId   string
	messages chan *message
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

// publishMessage publishes a message from a subscriber to the corresponding subscriber pair.
func (h *Handlers) publishMessage(msg *message, s *subscriber) error {
	h.subscribersMu.Lock()
	defer h.subscribersMu.Unlock()

	if err := h.publishLimiter.Wait(context.Background()); err != nil {
		return fmt.Errorf("message %s can not be published, limiter error: %w", msg, err)
	}

	// Message can be published to the paired subscriber only and only if pair exists
	sPair, ok := h.subscribers[s.pairId]
	if !ok {
		return fmt.Errorf("message %s can not be published, no available pair to send message to", msg)
	}

	sPair.messages <- msg
	h.log.Debugw("message sent successfully", "fromId", s.id, "toId", sPair.id)

	return nil
}

// notifySubscriber notifies subscriber directly about service state changes.
func (h *Handlers) notifySubscriber(ctx context.Context, conn *websocket.Conn, sId string, content string) {
	msg := &message{Content: content}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		h.log.Warnw("subscriber can not be notified due to error", "subscriberId", sId, "error", err)
	}
}

// linkSubscribers links two available subscribers with each other as a pair.
func (h *Handlers) linkSubscribers(sId, sPairId string) {
	h.subscribersMu.Lock()
	defer h.subscribersMu.Unlock()

	h.subscribers[sPairId].pairId = sId
	h.subscribers[sId].pairId = sPairId

	h.log.Debugw("successfully linked two subscribers",
		"firstId", h.subscribers[sId].id,
		"secondId", h.subscribers[sId].pairId)
}

// assignSubscriber assigns an available subscriber to a newcomer subscriber.
func (h *Handlers) assignSubscriber(ctx context.Context, s *subscriber) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ticker := time.NewTicker(checkSubAvailabilityMilliseconds * time.Millisecond)
	defer ticker.Stop()

	isFound := make(chan bool, 1)

	go func() {
		for range ticker.C {
			// Stop search If the current subscriber already has an assigned pair, mark as found
			if s.pairId != "" {
				h.log.Debugw("subscriber has a pair", "subscriberId", s.id, "pairId", s.pairId)
				isFound <- true
				return
			}
			select {
			case <-ctx.Done():
				h.log.Debugw("pair search is finished (found or timed out)", "subscriberId", s.id, "reason", "foundOrTimeout")
				return
			default:
				h.log.Debugw("search for a pair to assign to a subscriber", "subscriberId", s.id)
				for idAnother, sAnother := range h.subscribers {
					// Skip another subscriber if he has a pair or if it is a current subscriber
					if sAnother.pairId != "" || idAnother == s.id {
						continue
					}

					// The current subscriber and another one are both able to be linked with each other, mark as found
					h.linkSubscribers(s.id, sAnother.id)
					isFound <- true
				}
			}
		}
	}()

	select {
	case <-isFound:
		h.log.Debugw("pair found", "subscriberId", s.id, "pairId", s.pairId)
		return nil
	case <-time.After(checkSubAvailabilityMaxTimeSeconds * time.Second):
		h.log.Debugw(searchPairTimeoutError, "subscriberId", s.id)
		return errors.New(searchPairTimeoutError)
	}
}

// addSubscriber adds a subscriber.
func (h *Handlers) addSubscriber(s *subscriber) {
	h.subscribersMu.Lock()
	h.subscribers[s.id] = s
	h.subscribersMu.Unlock()
}

// deleteSubscriber deletes the given subscriber.
func (h *Handlers) deleteSubscriber(s *subscriber) {
	h.subscribersMu.Lock()
	delete(h.subscribers, s.id)
	defer h.subscribersMu.Unlock()
}

// readMessages reads messages from a subscriber connection and publishes them to the message channel.
func (h *Handlers) readMessages(ctx context.Context, conn *websocket.Conn, s *subscriber) context.Context {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		for {
			select {
			case <-ctx.Done():
				h.log.Debugw("read context is done", "subscriberId", s.id)
				return
			default:
				msg := &message{}
				if err := wsjson.Read(ctx, conn, &msg); err != nil {
					// Stop the read and write context if subscriber is closed the connection
					if errors.As(err, &websocket.CloseError{}) {
						h.log.Warnw("message can not be read, subscriber closed the connection", "subscriberId", s.id)
						cancel()
						return
					}

					// Skip the message if it can not be read or processed; take the next one
					h.log.Warnw("message can not be read due to error", "subscriberId", s.id, "error", err)
					continue
				}

				h.log.Debugw("successfully read the message from a subscriber", "subscriberId", s.id, "message", msg)

				if err := h.publishMessage(msg, s); err != nil {
					// Skip the message if it can not be published or processed; take the next one
					h.log.Warnw("message can not be published due to error", "subscriberId", s.id, "error", err)
					continue
				}
			}
		}
	}()

	return ctx
}

// writeMessages writes messages from the message channel to the subscriber connection.
func (h *Handlers) writeMessages(ctx context.Context, conn *websocket.Conn, s *subscriber) {
	go func() {
		for {
			select {
			case msg := <-s.messages:
				if err := wsjson.Write(ctx, conn, msg); err != nil {
					// Skip the message if it can not be written or processed; take the next one
					h.log.Warnw("message can not be written due to error", "subscriberId", s.id, "error", err)
					continue
				}
				h.log.Debugw("successfully wrote message to a subscriber", "subscriberId", s.id, "message", msg)
			case <-ctx.Done():
				h.log.Debugw("write context is done", "subscriberId", s.id)
				return
			}
		}
	}()
}

// Subscribe subscribes a client to the message channel to read and write messages from it
func (h *Handlers) Subscribe(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return err
	}

	defer conn.Close(websocket.StatusNormalClosure, "")

	s := &subscriber{
		id:       r.RemoteAddr,
		messages: make(chan *message, h.subscriberMessageBuffer),
	}

	h.addSubscriber(s)
	defer h.deleteSubscriber(s)

	h.notifySubscriber(ctx, conn, s.id, "wait for any available subscriber to chat with...")

	// Assign a subscriber pair to chat with.
	// When pair is successfully assigned, we can read messages from the pair and write messages to the pair
	if err = h.assignSubscriber(ctx, s); err != nil {
		h.log.Debugw("no available subscribers", "subscriberId", s.id, "error", err)
		h.notifySubscriber(ctx, conn, s.id, "no available subscribers; please, try again later.")

		if err = conn.Close(websocket.StatusTryAgainLater, "no available subscribers"); err != nil {
			h.log.Warnw("socket connection can not be closed", "subscriberId", s.id, "error", err)
		}

		return nil
	}

	h.notifySubscriber(ctx, conn, s.id, "subscriber is found. Say Hi!")

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
