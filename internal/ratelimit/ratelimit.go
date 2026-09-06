// Package ratelimit implements the self-throttling used to keep the archiver a
// polite client: a global minimum gap between HTTP requests, a global
// bandwidth cap, a pause gate, and a cool-down that is triggered when the
// server pushes back (429/503/403). Every knob can be adjusted while a run is
// in progress, which is what the GUI sliders do.
package ratelimit

import (
	"context"
	"io"
	"sync"
	"time"
)

// Limits is the user-adjustable throttle configuration.
type Limits struct {
	// RequestGap is the minimum time between the start of any two HTTP
	// requests, across all workers. 0 disables spacing.
	RequestGap time.Duration
	// Bandwidth caps total download throughput in bytes/second. 0 = unlimited.
	Bandwidth int64
	// EpisodeGap is a pause each worker takes after finishing one file before
	// starting the next. 0 disables it.
	EpisodeGap time.Duration
}

// Limiter is safe for concurrent use.
type Limiter struct {
	mu   sync.Mutex
	cond *sync.Cond

	limits Limits

	nextRequest   time.Time
	cooldownUntil time.Time
	paused        bool

	// token bucket for bandwidth
	tokens     float64
	lastRefill time.Time

	// stats
	requests int64
	bytes    int64
}

// New creates a limiter with the given limits.
func New(l Limits) *Limiter {
	lim := &Limiter{limits: l, lastRefill: time.Now()}
	lim.cond = sync.NewCond(&lim.mu)
	return lim
}

// Set replaces the limits atomically; waiting callers pick up the change.
func (l *Limiter) Set(n Limits) {
	l.mu.Lock()
	l.limits = n
	l.cond.Broadcast()
	l.mu.Unlock()
}

// Limits returns the current limits.
func (l *Limiter) Limits() Limits {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limits
}

// Pause blocks all requests and transfers until Resume is called.
func (l *Limiter) Pause() {
	l.mu.Lock()
	l.paused = true
	l.mu.Unlock()
}

// Resume lifts a Pause.
func (l *Limiter) Resume() {
	l.mu.Lock()
	l.paused = false
	l.cond.Broadcast()
	l.mu.Unlock()
}

// Paused reports whether the limiter is paused.
func (l *Limiter) Paused() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.paused
}

// Cooldown blocks all requests for d (extending any current cool-down).
func (l *Limiter) Cooldown(d time.Duration) {
	l.mu.Lock()
	until := time.Now().Add(d)
	if until.After(l.cooldownUntil) {
		l.cooldownUntil = until
	}
	l.mu.Unlock()
}

// CooldownUntil returns the time the current cool-down ends (zero if none).
func (l *Limiter) CooldownUntil() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Now().Before(l.cooldownUntil) {
		return l.cooldownUntil
	}
	return time.Time{}
}

// ClearCooldown cancels any pending cool-down.
func (l *Limiter) ClearCooldown() {
	l.mu.Lock()
	l.cooldownUntil = time.Time{}
	l.cond.Broadcast()
	l.mu.Unlock()
}

// Stats returns the number of requests made and bytes transferred through
// this limiter.
func (l *Limiter) Stats() (requests, bytes int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.requests, l.bytes
}

// WaitGate blocks while paused or cooling down. It returns ctx.Err() if the
// context is cancelled first.
func (l *Limiter) WaitGate(ctx context.Context) error {
	for {
		l.mu.Lock()
		now := time.Now()
		if !l.paused && !now.Before(l.cooldownUntil) {
			l.mu.Unlock()
			return nil
		}
		var wait time.Duration
		if l.paused {
			wait = 250 * time.Millisecond
		} else {
			wait = l.cooldownUntil.Sub(now)
			if wait > 250*time.Millisecond {
				wait = 250 * time.Millisecond
			}
		}
		l.mu.Unlock()
		if err := sleepCtx(ctx, wait); err != nil {
			return err
		}
	}
}

// WaitRequest blocks until a new HTTP request may be started: it honours the
// pause gate, cool-down, and the inter-request gap.
func (l *Limiter) WaitRequest(ctx context.Context) error {
	for {
		if err := l.WaitGate(ctx); err != nil {
			return err
		}
		l.mu.Lock()
		now := time.Now()
		if l.paused || now.Before(l.cooldownUntil) {
			l.mu.Unlock()
			continue
		}
		if !now.Before(l.nextRequest) {
			l.nextRequest = now.Add(l.limits.RequestGap)
			l.requests++
			l.mu.Unlock()
			return nil
		}
		wait := l.nextRequest.Sub(now)
		l.mu.Unlock()
		if wait > 250*time.Millisecond {
			wait = 250 * time.Millisecond // re-check limits/pause frequently
		}
		if err := sleepCtx(ctx, wait); err != nil {
			return err
		}
	}
}

// WaitEpisodeGap sleeps for the configured per-worker gap between files.
func (l *Limiter) WaitEpisodeGap(ctx context.Context) error {
	l.mu.Lock()
	gap := l.limits.EpisodeGap
	l.mu.Unlock()
	if gap <= 0 {
		return nil
	}
	deadline := time.Now().Add(gap)
	for time.Now().Before(deadline) {
		if err := sleepCtx(ctx, min(250*time.Millisecond, time.Until(deadline))); err != nil {
			return err
		}
	}
	return nil
}

// take blocks until n bytes of bandwidth are available and consumes them.
func (l *Limiter) take(ctx context.Context, n int) error {
	for {
		if err := l.WaitGate(ctx); err != nil {
			return err
		}
		l.mu.Lock()
		bw := l.limits.Bandwidth
		if bw <= 0 {
			l.bytes += int64(n)
			l.mu.Unlock()
			return nil
		}
		now := time.Now()
		elapsed := now.Sub(l.lastRefill).Seconds()
		l.lastRefill = now
		l.tokens += elapsed * float64(bw)
		// allow up to one second of burst
		if l.tokens > float64(bw) {
			l.tokens = float64(bw)
		}
		if l.tokens >= float64(n) {
			l.tokens -= float64(n)
			l.bytes += int64(n)
			l.mu.Unlock()
			return nil
		}
		deficit := float64(n) - l.tokens
		wait := time.Duration(deficit / float64(bw) * float64(time.Second))
		l.mu.Unlock()
		if wait > 250*time.Millisecond {
			wait = 250 * time.Millisecond
		}
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		if err := sleepCtx(ctx, wait); err != nil {
			return err
		}
	}
}

// Reader wraps r so that reads are throttled by the bandwidth cap and blocked
// while paused / cooling down. Cancelling ctx aborts the next read.
func (l *Limiter) Reader(ctx context.Context, r io.Reader) io.Reader {
	return &throttledReader{ctx: ctx, r: r, l: l}
}

type throttledReader struct {
	ctx context.Context
	r   io.Reader
	l   *Limiter
}

const chunk = 32 * 1024

func (t *throttledReader) Read(p []byte) (int, error) {
	if len(p) > chunk {
		p = p[:chunk]
	}
	if err := t.l.take(t.ctx, len(p)); err != nil {
		return 0, err
	}
	n, err := t.r.Read(p)
	if n < len(p) {
		// give back what we didn't use so short reads don't over-charge
		t.l.mu.Lock()
		if t.l.limits.Bandwidth > 0 {
			t.l.tokens += float64(len(p) - n)
		}
		t.l.bytes -= int64(len(p) - n)
		t.l.mu.Unlock()
	}
	return n, err
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
