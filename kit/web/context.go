package web

import (
	"context"
	"time"
)

type ctxKey int

const key ctxKey = 1

// Values represent state for each request.
type Values struct {
	Now        time.Time
	StatusCode int
}

// GetValues returns the values from the context.
func GetValues(ctx context.Context) *Values {
	v, ok := ctx.Value(key).(*Values)
	if !ok {
		return &Values{
			Now: time.Now(),
		}
	}

	return v
}
