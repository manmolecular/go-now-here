package mid

import (
	"context"
	"fmt"
	"github.com/manmolecular/go-now-here/kit/web"
	"go.uber.org/zap"
	"net/http"
	"time"
)

// Logger writes information about the request to the logs.
func Logger(log *zap.SugaredLogger) web.Middleware {
	m := func(handler web.Handler) web.Handler {
		h := func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
			v := web.GetValues(ctx)

			path := r.URL.Path
			if r.URL.RawQuery != "" {
				path = fmt.Sprintf("%s?%s", path, r.URL.RawQuery)
			}

			log.Infow("request started", "method", r.Method, "path", path,
				"remoteaddr", r.RemoteAddr)

			err := handler(ctx, w, r)

			log.Infow("request completed", "method", r.Method, "path", path,
				"remoteaddr", r.RemoteAddr, "statuscode", v.StatusCode, "since", time.Since(v.Now))

			return err
		}

		return h
	}

	return m
}
