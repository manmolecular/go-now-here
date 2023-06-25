// Package v1 contains the full set of handler functions and routes
// supported by the v1 web api.
package v1

import (
	"github.com/manmolecular/go-now-here/app/services/api/handlers/v1/chatgrp"
	"github.com/manmolecular/go-now-here/app/services/api/handlers/v1/healthcheckgrp"
	"github.com/manmolecular/go-now-here/app/services/api/handlers/v1/uigrp"
	"github.com/manmolecular/go-now-here/business/core/subscriber"
	"github.com/manmolecular/go-now-here/kit/web"
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

	healthcheck := healthcheckgrp.New(cfg.Log)
	chat := chatgrp.New(cfg.Log, subscriber.NewSubscribersPool(cfg.Log))
	ui := uigrp.New(cfg.Log)

	app.Handle(http.MethodGet, version, "/liveness", healthcheck.Liveness)
	app.Handle(http.MethodGet, version, "/ws/chat", chat.Subscribe)

	// Serve simple UI for interaction
	app.Handle(http.MethodGet, "", "/", ui.Index)
}
