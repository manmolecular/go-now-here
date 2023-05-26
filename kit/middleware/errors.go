package middleware

import (
	"context"
	"github.com/manmolecular/go-now-here/kit/web"
	"net/http"

	"go.uber.org/zap"
)

// Errors handles errors coming out of the call chain. It detects normal
// application errors which are used to respond to the client in a uniform way.
// Unexpected errors (status >= 500) are logged.
func Errors(log *zap.SugaredLogger) web.Middleware {
	m := func(handler web.Handler) web.Handler {
		h := func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
			if err := handler(ctx, w, r); err != nil {
				log.Errorw("ERROR", "message", err)

				if err := web.Respond(ctx, w, "", http.StatusInternalServerError); err != nil {
					return err
				}
			}

			return nil
		}

		return h
	}

	return m
}
