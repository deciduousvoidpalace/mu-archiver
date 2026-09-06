package ratelimit

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"
)

func TestRequestGap(t *testing.T) {
	l := New(Limits{RequestGap: 40 * time.Millisecond})
	ctx := context.Background()
	start := time.Now()
	for i := 0; i < 4; i++ {
		if err := l.WaitRequest(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if el := time.Since(start); el < 110*time.Millisecond {
		t.Fatalf("4 requests with 40ms gap finished too fast: %v", el)
	}
	if n, _ := l.Stats(); n != 4 {
		t.Fatalf("requests = %d, want 4", n)
	}
}

func TestBandwidthCap(t *testing.T) {
	l := New(Limits{Bandwidth: 200 * 1024}) // 200 KiB/s
	data := bytes.Repeat([]byte("x"), 300*1024)
	start := time.Now()
	n, err := io.Copy(io.Discard, l.Reader(context.Background(), bytes.NewReader(data)))
	if err != nil || n != int64(len(data)) {
		t.Fatalf("copy: n=%d err=%v", n, err)
	}
	// 300 KiB at 200 KiB/s with a 1s burst allowance => at least ~0.5s
	if el := time.Since(start); el < 400*time.Millisecond {
		t.Fatalf("throttle ineffective: %v", el)
	}
}

func TestPauseAndCancel(t *testing.T) {
	l := New(Limits{})
	l.Pause()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if err := l.WaitRequest(ctx); err == nil {
		t.Fatal("expected ctx error while paused")
	}
	l.Resume()
	if err := l.WaitRequest(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCooldown(t *testing.T) {
	l := New(Limits{})
	l.Cooldown(60 * time.Millisecond)
	start := time.Now()
	if err := l.WaitRequest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("cooldown not honoured")
	}
	if !l.CooldownUntil().IsZero() {
		t.Fatal("cooldown should be over")
	}
}
