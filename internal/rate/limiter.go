package rate

import (
	"context"
	"fmt"
	"time"
)

// Limiter gates outbound API calls so we respect Gmail rate limits.
type Limiter interface {
	Wait(ctx context.Context) error
}

// TokenBucket implements a simple fixed-rate token bucket limiter.
type TokenBucket struct {
	ticker   *time.Ticker
	tokens   chan struct{}
	stopDone chan struct{}
}

// NewTokenBucket returns a limiter that releases rps tokens per second.
func NewTokenBucket(rps int) *TokenBucket {
	if rps <= 0 {
		rps = 1
	}
	tb := &TokenBucket{
		ticker:   time.NewTicker(time.Second / time.Duration(rps)),
		tokens:   make(chan struct{}, rps),
		stopDone: make(chan struct{}),
	}
	// allow the first call to proceed immediately
	tb.tokens <- struct{}{}
	go tb.run()
	return tb
}

func (t *TokenBucket) run() {
	defer close(t.stopDone)
	for range t.ticker.C {
		select {
		case t.tokens <- struct{}{}:
		default:
		}
	}
}

// Wait blocks until a token is available or the context is canceled.
func (t *TokenBucket) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("rate wait canceled: %w", ctx.Err())
	case <-t.tokens:
		return nil
	}
}

// Stop releases resources held by the limiter.
func (t *TokenBucket) Stop() {
	t.ticker.Stop()
	<-t.stopDone
}

var _ Limiter = (*TokenBucket)(nil)
