// Package chatgrp maintains the group of handlers for chat handling via websockets.
package chatgrp

import (
	"context"
	"errors"
	"fmt"
	"github.com/manmolecular/go-now-here/business/core/message"
	"github.com/manmolecular/go-now-here/business/core/subscriber"
	"go.uber.org/zap"
	"net/http"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// Available sender names
const (
	fromServiceName  = "service"
	fromStrangerName = "stranger"
)

// Notification statuses
const (
	statusOk    = "ok"
	statusError = "error"
)

// Handlers manages the set of chat endpoints.
type Handlers struct {
	log         *zap.SugaredLogger
	subscribers *subscriber.SubscribersPool
}

// New constructs a Handlers api for the chat group.
func New(log *zap.SugaredLogger, subscribers *subscriber.SubscribersPool) *Handlers {
	return &Handlers{
		log:         log,
		subscribers: subscribers,
	}
}

// notifySubscriber notifies subscriber directly about service state changes.
func (h *Handlers) notifySubscriber(ctx context.Context, conn *websocket.Conn, sId, content, status string) {
	msg := message.NewMessage(content, fromServiceName, status)
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		h.log.Warnw("subscriber can not be notified due to error", "subscriberId", sId, "error", err)
	}
}

// readMessages reads messages from a subscriber connection and publishes them to the message channel.
func (h *Handlers) readMessages(ctx context.Context, conn *websocket.Conn, s *subscriber.Subscriber) context.Context {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		for {
			select {
			case <-ctx.Done():
				h.log.Debugw("read context is done", "subscriberId", s.Id)
				return
			default:
				msg := message.NewMessage("", fmt.Sprintf("%s-%s", fromStrangerName, s.Id[:3]), statusOk)
				if err := wsjson.Read(ctx, conn, &msg); err != nil {
					// Stop the read and write context if subscriber is closed the connection
					if errors.As(err, &websocket.CloseError{}) {
						h.log.Warnw("message can not be read, subscriber closed the connection", "subscriberId", s.Id)
						cancel()
						return
					}

					// Skip the message if it can not be read or processed; take the next one
					h.log.Warnw("message can not be read due to error", "subscriberId", s.Id, "error", err)
					continue
				}

				h.log.Debugw("successfully read the message from a subscriber", "subscriberId", s.Id, "message", msg)

				if err := h.subscribers.PublishMessage(msg, s); err != nil {
					// Skip the message if it can not be published or processed; take the next one
					h.log.Warnw("message can not be published due to error", "subscriberId", s.Id, "error", err)
					continue
				}
			}
		}
	}()

	return ctx
}

// writeMessages writes messages from the message channel to the subscriber connection.
func (h *Handlers) writeMessages(ctx context.Context, conn *websocket.Conn, s *subscriber.Subscriber) {
	go func() {
		for {
			select {
			case msg := <-s.Messages:
				if err := wsjson.Write(ctx, conn, msg); err != nil {
					// Skip the message if it can not be written or processed; take the next one
					h.log.Warnw("message can not be written due to error", "subscriberId", s.Id, "error", err)
					continue
				}
				h.log.Debugw("successfully wrote message to a subscriber", "subscriberId", s.Id, "message", msg)
			case <-ctx.Done():
				h.log.Debugw("write context is done", "subscriberId", s.Id)
				return
			}
		}
	}()
}

// Subscribe subscribes a client to the message channel to read and write messages from it.
func (h *Handlers) Subscribe(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return err
	}

	defer func() {
		if err = conn.Close(websocket.StatusNormalClosure, ""); err != nil {
			h.log.Warnw("socket connection can not be closed", "error", err)
		}
	}()

	s := subscriber.NewSubscriber(r.RemoteAddr)

	h.subscribers.AddSubscriber(s)
	defer h.subscribers.DeleteSubscriber(s)

	h.notifySubscriber(ctx, conn, s.Id, "wait for any available subscriber to chat with...", statusOk)

	// Assign a subscriber pair to chat with.
	// When pair is successfully assigned, we can read messages from the pair and write messages to the pair
	if err = h.subscribers.AssignSubscriber(s); err != nil {
		h.log.Debugw("no available subscribers", "subscriberId", s.Id, "error", err)
		h.notifySubscriber(ctx, conn, s.Id, "no available subscribers; please, try again later.", statusError)

		if err = conn.Close(websocket.StatusTryAgainLater, "no available subscribers"); err != nil {
			h.log.Warnw("socket connection can not be closed", "subscriberId", s.Id, "error", err)
		}

		return nil
	}

	h.notifySubscriber(ctx, conn, s.Id, "subscriber is found. Say Hi!", statusOk)

	// Read messages in a goroutine, ctx will be canceled if client closed the connection
	ctx = h.readMessages(ctx, conn, s)

	// Write messages to a client from the message channel in goroutine until the read context is done
	h.writeMessages(ctx, conn, s)

	// Block handler while reader and writer goroutines are performing operations until the read context is done
	select {
	case <-ctx.Done():
		h.log.Debugw("client closed the connection", "subscriberId", s.Id)
	}

	return nil
}
