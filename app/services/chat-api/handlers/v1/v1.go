// Package v1 contains the full set of handler functions and routes
// supported by the v1 web api.
package v1

import (
	"github.com/ardanlabs/service/foundation/web"
	"github.com/manmolecular/go-now-here/app/services/chat-api/handlers/v1/chat"
	"github.com/manmolecular/go-now-here/app/services/chat-api/handlers/v1/healthcheck"
	"go.uber.org/zap"
	"net/http"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log *zap.SugaredLogger
}

// Routes binds all the version 1 routes.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	health := healthcheck.New(cfg.Log)
	wsChat := chat.New(cfg.Log)

	app.Handle(http.MethodGet, version, "/liveness", health.Liveness)
	app.Handle(http.MethodGet, version, "/ws/chat", wsChat.Chat)
}
