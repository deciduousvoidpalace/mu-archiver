package downloader

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mu-dl/internal/httpx"
	"mu-dl/internal/ratelimit"
)

// flakyServer serves data with Range support and drops the connection after
// cutAfter bytes on the first request.
func flakyServer(data []byte, cutAfter int) (*httptest.Server, *int) {
	calls := 0
	h := func(w http.ResponseWriter, r *http.Request) {
		calls++
		start := 0
		if rg := r.Header.Get("Range"); strings.HasPrefix(rg, "bytes=") {
			start, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"))
			if start >= len(data) {
				w.Header().Set("Content-Range", "bytes */"+strconv.Itoa(len(data)))
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			w.Header().Set("Content-Range", "bytes "+strconv.Itoa(start)+"-"+strconv.Itoa(len(data)-1)+"/"+strconv.Itoa(len(data)))
			w.Header().Set("Content-Length", strconv.Itoa(len(data)-start))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.Header().Set("Content-Type", "audio/mpeg")
		}
		if calls == 1 && cutAfter > 0 {
			w.Write(data[start : start+cutAfter])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// abort the connection mid-body
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					conn.Close()
				}
			}
			return
		}
		w.Write(data[start:])
	}
	return httptest.NewServer(http.HandlerFunc(h)), &calls
}

func TestResumeAfterInterruption(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789"), 20000) // 200 KB
	srv, calls := flakyServer(data, 50000)
	defer srv.Close()
	c, _ := httpx.New(true, 10*time.Second, ratelimit.New(ratelimit.Limits{}))
	dst := filepath.Join(t.TempDir(), "Season 01", "1.01 - x.mp3")
	var last *Transfer
	n, err := Download(context.Background(), c, srv.URL+"/x.mp3", dst, Options{OnProgress: func(tr *Transfer) { last = tr }})
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if n != int64(len(data)) {
		t.Fatalf("size = %d, want %d", n, len(data))
	}
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(got, data) {
		t.Fatal("content mismatch after resume")
	}
	if *calls < 2 {
		t.Fatalf("expected a resumed second request, calls=%d", *calls)
	}
	if _, err := os.Stat(dst + ".part"); err == nil {
		t.Fatal(".part left behind")
	}
	if p := last.Snapshot(); p.Done != int64(len(data)) || p.Total != int64(len(data)) {
		t.Fatalf("progress snapshot = %+v", p)
	}
}

func TestCancelKeepsPartial(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 400*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Write(data)
	}))
	defer srv.Close()
	lim := ratelimit.New(ratelimit.Limits{Bandwidth: 100 * 1024})
	c, _ := httpx.New(true, 10*time.Second, lim)
	ctx, cancel := context.WithCancel(context.Background())
	dst := filepath.Join(t.TempDir(), "y.mp3")
	go func() { time.Sleep(900 * time.Millisecond); cancel() }()
	_, err := Download(ctx, c, srv.URL, dst, Options{})
	if err == nil || ctx.Err() == nil {
		t.Fatalf("expected cancellation, err=%v", err)
	}
	fi, err := os.Stat(dst + ".part")
	if err != nil || fi.Size() == 0 || fi.Size() >= int64(len(data)) {
		t.Fatalf("expected a partial .part file, err=%v", err)
	}
}

func TestHTMLResponseIsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<html>login</html>"))
	}))
	defer srv.Close()
	c, _ := httpx.New(true, 5*time.Second, nil)
	start := time.Now()
	_, err := Download(context.Background(), c, srv.URL, filepath.Join(t.TempDir(), "z.mp3"), Options{})
	if err == nil || !strings.Contains(err.Error(), "HTML") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("permanent error should not retry with backoff")
	}
}

func TestSizeMismatchRetriesThenFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write(bytes.Repeat([]byte("a"), 100))
	}))
	defer srv.Close()
	c, _ := httpx.New(true, 5*time.Second, nil)
	n, err := Download(context.Background(), c, srv.URL, filepath.Join(t.TempDir(), "ok.mp3"), Options{Retries: 1})
	if err != nil || n != 100 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
