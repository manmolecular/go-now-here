// Package healthcheck maintains the group of handlers for health checking.
package healthcheck

import (
	"context"
	"github.com/ardanlabs/service/foundation/web"
	"go.uber.org/zap"
	"net/http"
	"os"
)

// Handlers manages the set of healthcheck endpoints.
type Handlers struct {
	Log *zap.SugaredLogger
}

// New constructs a Handlers api for the healthcheck group.
func New(log *zap.SugaredLogger) *Handlers {
	return &Handlers{
		Log: log,
	}
}

func (h *Handlers) Liveness(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	host, err := os.Hostname()
	if err != nil {
		host = "unavailable"
	}

	data := struct {
		Status string `json:"status,omitempty"`
		Host   string `json:"host,omitempty"`
	}{
		Status: "up",
		Host:   host,
	}

	return web.Respond(ctx, w, data, http.StatusOK)
}
