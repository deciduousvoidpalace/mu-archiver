// Package downloader fetches episode enclosures with HTTP Range resume,
// retries with backoff, atomic writes (download to .part, then rename),
// size verification, bandwidth throttling, pause and cancellation, and
// per-transfer progress reporting.
package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mu-dl/internal/httpx"
)

// Progress is a snapshot of one transfer.
type Progress struct {
	Name    string
	Done    int64 // bytes on disk (including resumed prefix)
	Total   int64 // expected total, 0 if unknown
	Speed   float64
	Started time.Time
	Attempt int
}

// Transfer tracks a single in-flight download and is safe to read from other
// goroutines via Snapshot.
type Transfer struct {
	name    string
	done    atomic.Int64
	total   atomic.Int64
	started time.Time
	attempt atomic.Int32

	mu    sync.Mutex
	lastT time.Time
	lastN int64
	speed float64
}

// Snapshot returns the current progress.
func (t *Transfer) Snapshot() Progress {
	t.mu.Lock()
	sp := t.speed
	t.mu.Unlock()
	return Progress{Name: t.name, Done: t.done.Load(), Total: t.total.Load(), Speed: sp, Started: t.started, Attempt: int(t.attempt.Load())}
}

func (t *Transfer) add(n int) {
	d := t.done.Add(int64(n))
	now := time.Now()
	t.mu.Lock()
	if t.lastT.IsZero() {
		t.lastT, t.lastN = now, d
	} else if el := now.Sub(t.lastT); el >= 500*time.Millisecond {
		inst := float64(d-t.lastN) / el.Seconds()
		if t.speed == 0 {
			t.speed = inst
		} else {
			t.speed = 0.7*t.speed + 0.3*inst
		}
		t.lastT, t.lastN = now, d
	}
	t.mu.Unlock()
}

// ErrSizeMismatch is returned when the server's Content-Length and the bytes
// on disk disagree after a complete transfer.
var ErrSizeMismatch = errors.New("downloaded size does not match server's Content-Length")

// Options tune a single download.
type Options struct {
	// Retries is the number of attempts (default 5).
	Retries int
	// OnProgress, when set, receives the Transfer for live snapshots.
	OnProgress func(*Transfer)
}

// Download fetches url to dst, resuming a prior .part file when possible.
// It returns the final size on disk.
func Download(ctx context.Context, c *httpx.Client, url, dst string, opts Options) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	retries := opts.Retries
	if retries <= 0 {
		retries = 5
	}
	part := dst + ".part"
	tr := &Transfer{name: filepath.Base(dst), started: time.Now()}
	if opts.OnProgress != nil {
		opts.OnProgress(tr)
	}

	var lastErr error
	backoff := 3 * time.Second
	for attempt := 0; attempt < retries; attempt++ {
		tr.attempt.Store(int32(attempt + 1))
		if attempt > 0 {
			if err := sleepCtx(ctx, backoff); err != nil {
				return 0, err
			}
			if backoff < 2*time.Minute {
				backoff *= 2
			}
		}
		if err := c.Limiter.WaitGate(ctx); err != nil {
			return 0, err
		}

		size, err := attemptOnce(ctx, c, url, dst, part, tr)
		if err == nil {
			return size, nil
		}
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		lastErr = err
		var perm *permanentError
		if errors.As(err, &perm) {
			return 0, perm.err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("download failed")
	}
	return 0, lastErr
}

type permanentError struct{ err error }

func (p *permanentError) Error() string { return p.err.Error() }

func attemptOnce(ctx context.Context, c *httpx.Client, url, dst, part string, tr *Transfer) (int64, error) {
	var offset int64
	if fi, err := os.Stat(part); err == nil {
		offset = fi.Size()
	}
	tr.done.Store(offset)

	req, err := c.NewRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, &permanentError{err}
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var expected int64 // full file size if known
	switch resp.StatusCode {
	case http.StatusOK:
		// server ignored Range (or none requested): rewrite from scratch
		offset = 0
		tr.done.Store(0)
		expected = resp.ContentLength
	case http.StatusPartialContent:
		expected = totalFromContentRange(resp.Header.Get("Content-Range"))
		if expected <= 0 && resp.ContentLength > 0 {
			expected = offset + resp.ContentLength
		}
	case http.StatusRequestedRangeNotSatisfiable:
		// our .part is already >= full size; verify against the real size
		total := totalFromContentRange(resp.Header.Get("Content-Range"))
		if total > 0 && offset != total {
			os.Remove(part)
			return 0, fmt.Errorf("partial file larger than the remote file; restarting")
		}
		if err := os.Rename(part, dst); err != nil {
			return 0, err
		}
		return offset, nil
	default:
		err := fmt.Errorf("server returned %s", resp.Status)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusRequestTimeout {
			return 0, &permanentError{err}
		}
		return 0, err
	}
	if expected > 0 {
		tr.total.Store(expected)
	}
	if ct := strings.ToLower(resp.Header.Get("Content-Type")); strings.HasPrefix(ct, "text/html") {
		return 0, &permanentError{fmt.Errorf("server returned an HTML page instead of audio (access denied or expired link)")}
	}

	flag := os.O_CREATE | os.O_WRONLY
	if offset > 0 {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(part, flag, 0o644)
	if err != nil {
		return 0, &permanentError{err}
	}
	src := c.Limiter.Reader(ctx, resp.Body)
	written, copyErr := copyWithProgress(f, src, tr)
	closeErr := f.Close()
	if copyErr != nil {
		return 0, copyErr // partial data remains in .part for the next resume
	}
	if closeErr != nil {
		return 0, closeErr
	}
	got := offset + written
	if expected > 0 && got != expected {
		if got > expected {
			os.Remove(part)
		}
		return 0, fmt.Errorf("%w (got %d, want %d)", ErrSizeMismatch, got, expected)
	}
	if err := os.Rename(part, dst); err != nil {
		return 0, err
	}
	return got, nil
}

func copyWithProgress(dst io.Writer, src io.Reader, tr *Transfer) (int64, error) {
	buf := make([]byte, 64*1024)
	var total int64
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
			tr.add(n)
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, rerr
		}
	}
}

// totalFromContentRange parses "bytes 100-999/1000" -> 1000.
func totalFromContentRange(h string) int64 {
	i := strings.LastIndexByte(h, '/')
	if i < 0 {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(h[i+1:]), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
