// internal/rate/limiter.go
package rate

import (
	"context"
	"time"
)

type Limiter interface {
	Wait(ctx context.Context) error
}

type TokenBucket struct {
	ticker *time.Ticker
	ch     chan struct{}
}

func NewTokenBucket(rps int) *TokenBucket {
	tb := &TokenBucket{
		ticker: time.NewTicker(time.Second / time.Duration(max(1, rps))),
		ch:     make(chan struct{}, rps),
	}
	go func() {
		for range tb.ticker.C {
			select {
			case tb.ch <- struct{}{}:
			default:
			}
		}
	}()
	return tb
}
func (t *TokenBucket) Wait(ctx context.Context) error {
	select {
	case <-t.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
