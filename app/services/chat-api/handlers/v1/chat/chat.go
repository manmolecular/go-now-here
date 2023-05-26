// Package chat maintains the group of handlers for chat handling via websockets.
package chat

import (
	"context"
	"go.uber.org/zap"
	"net/http"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// Handlers manages the set of chat endpoints.
type Handlers struct {
	Log *zap.SugaredLogger
}

// New constructs a Handlers api for the chat group.
func New(log *zap.SugaredLogger) *Handlers {
	return &Handlers{
		Log: log,
	}
}

// Test with:
// let socket = new WebSocket("ws://localhost:3000/v1/ws/chat");
// socket.onopen = function(e) {
//  socket.send('{"test": "works"}');
// };

func (h *Handlers) Chat(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return err
	}

	var message interface{}
	if err = wsjson.Read(ctx, conn, &message); err != nil {
		return err
	}

	h.Log.Infof("received: %v", message)

	if err = wsjson.Write(ctx, conn, message); err != nil {
		return err
	}

	h.Log.Infof("sent: %v", message)

	conn.Close(websocket.StatusNormalClosure, "")

	return nil
}
