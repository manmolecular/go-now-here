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
	"time"
)

// Available sender names
const (
	fromService    = "service"
	fromSubscriber = "stranger"
)

// Notification statuses
const (
	statusOk    = "ok"
	statusError = "error"
)

const readMessagesIntervalMilliseconds = 100

// Handlers manages the set of chat endpoints.
type Handlers struct {
	log  *zap.SugaredLogger
	pool *subscriber.SubscribersPool
}

// New constructs a Handlers api for the chat group.
func New(log *zap.SugaredLogger, subscribers *subscriber.SubscribersPool) *Handlers {
	return &Handlers{
		log:  log,
		pool: subscribers,
	}
}

// notify notifies subscriber directly about service state changes.
func (h *Handlers) notify(ctx context.Context, conn *websocket.Conn, sId, content, status string) {
	msg := message.NewMessage(content, fromService, status)
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		h.log.Warnw("client can not be notified due to error", "subscriberId", sId, "error", err)
	}
}

// read reads messages from a subscriber connection and publishes them to the message channel.
func (h *Handlers) read(ctx context.Context, conn *websocket.Conn, s *subscriber.Subscriber) context.Context {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		ticker := time.NewTicker(readMessagesIntervalMilliseconds * time.Millisecond)
		defer ticker.Stop()

		for range ticker.C {
			select {
			case <-ctx.Done():
				h.log.Debugw("read context is done", "subscriberId", s.Id)
				return
			default:
				msg := message.NewMessage("", fmt.Sprintf("%s-%s", fromSubscriber, s.Id[:3]), statusOk)
				if err := wsjson.Read(ctx, conn, &msg); err != nil {
					// Stop the read and write context if subscriber is closed the connection
					if errors.As(err, &websocket.CloseError{}) {
						h.log.Warnw("message can not be read, subscriber closed the connection", "subscriberId", s.Id)
						cancel()
						return
					}

					// Check if the client closed the connection unexpectedly, no exact error - reader is not available
					if _, _, err = conn.Reader(ctx); err != nil {
						h.log.Warnw("message can not be read, subscriber unexpectedly closed the connection", "subscriberId", s.Id)
						cancel()
						return
					}

					// Skip the message if it can not be read or processed; take the next one
					h.log.Warnw("message can not be read due to error", "subscriberId", s.Id, "error", err)
					continue
				}

				h.log.Debugw("successfully read the message from a subscriber", "subscriberId", s.Id, "message", msg)

				if err := h.pool.PublishMessage(msg, s); err != nil {
					// Skip the message if it can not be published or processed; take the next one
					h.log.Warnw("message can not be published due to error", "subscriberId", s.Id, "error", err)
					continue
				}
			}
		}
	}()

	return ctx
}

// write writes messages from the message channel to the subscriber connection.
func (h *Handlers) write(ctx context.Context, conn *websocket.Conn, s *subscriber.Subscriber) context.Context {
	ctx, cancel := context.WithCancel(ctx)

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
			case <-s.PairDisconnected:
				// Start over, subscriber pair is disconnected and chat is no longer possible
				h.notify(ctx, conn, s.Id, "subscriber disconnected.", statusOk)
				h.log.Warnw("subscriber pair is disconnected", "subscriberId", s.Id)
				cancel()
			case <-ctx.Done():
				h.log.Debugw("write context is done", "subscriberId", s.Id)
				return
			}
		}
	}()

	return ctx
}

// Subscribe subscribes a client to the message channel to read and write messages from it.
func (h *Handlers) Subscribe(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})

	if err != nil {
		return err
	}

	defer func() {
		if err = conn.Close(websocket.StatusNormalClosure, ""); err != nil {
			h.log.Warnw("socket connection can not be closed", "error", err)
		}
	}()

	s := subscriber.NewSubscriber(r.RemoteAddr)

	h.pool.AddSubscriber(s)
	defer h.pool.DeleteSubscriber(s)

	h.notify(ctx, conn, s.Id, fmt.Sprintf("wait %d sec. for available subscriber to chat with...", subscriber.CheckSubAvailabilityMaxTimeSeconds), statusOk)

	// Assign a subscriber pair to chat with.
	// When pair is successfully assigned, we can read messages from the pair and write messages to the pair
	if err = h.pool.AssignSubscriber(s); err != nil {
		h.log.Debugw("no available subscribers", "subscriberId", s.Id, "error", err)
		h.notify(ctx, conn, s.Id, "no available subscribers.", statusError)

		if err = conn.Close(websocket.StatusTryAgainLater, "no available subscribers"); err != nil {
			h.log.Warnw("socket connection can not be closed", "subscriberId", s.Id, "error", err)
		}

		return nil
	}

	h.notify(ctx, conn, s.Id, "subscriber is found.", statusOk)

	// Read messages in a goroutine, ctx will be canceled if client closed the connection
	ctx = h.read(ctx, conn, s)

	// Write messages to a client from the message channel in goroutine until the read context is done
	ctx = h.write(ctx, conn, s)

	// Block handler while reader and writer goroutines are performing operations until the read context is done
	<-ctx.Done()
	h.log.Debugw("client closed the connection", "subscriberId", s.Id)

	return nil
}
