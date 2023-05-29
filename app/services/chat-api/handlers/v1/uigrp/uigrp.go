// Package uigrp serves static HTML files for UI
package uigrp

import (
	"context"
	"go.uber.org/zap"
	"net/http"
)

const uiFilePath = "./ui/index.html"

// Handlers manages the set of UI endpoints.
type Handlers struct {
	log          *zap.SugaredLogger
	staticServer http.Handler
}

// New constructs a Handlers api for the UI group.
func New(log *zap.SugaredLogger) *Handlers {
	return &Handlers{
		log: log,
	}
}

// Index serves the basic UI as html file
func (h *Handlers) Index(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	http.ServeFile(w, r, uiFilePath)
	return nil
}
