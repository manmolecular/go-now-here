// Package handlers handles different versions of the API.
package handlers

import (
	v1 "github.com/manmolecular/go-now-here/app/services/api/handlers/v1"
	"github.com/manmolecular/go-now-here/business/web/v1/mid"
	"github.com/manmolecular/go-now-here/kit/web"
	"go.uber.org/zap"
	"net/http"
	"os"
)

// Options represent optional parameters, such as CORS.
type Options struct{}

// APIMuxConfig contains all the mandatory systems required by handlers.
type APIMuxConfig struct {
	Shutdown chan os.Signal
	Log      *zap.SugaredLogger
}

func APIMux(cfg APIMuxConfig, options ...func(opts *Options)) http.Handler {
	var opts Options
	for _, option := range options {
		option(&opts)
	}

	app := web.NewApp(
		cfg.Shutdown,
		nil,
		mid.Logger(cfg.Log),
		mid.Errors(cfg.Log),
		mid.Panics(),
	)

	v1.Routes(app, v1.Config{
		Log: cfg.Log,
	})

	return app
}
