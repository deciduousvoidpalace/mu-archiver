// Package httpx provides the HTTP client shared by every part of the archiver:
// a persistent cookie jar, a browser-compatible User-Agent, context-aware
// requests that pass through the rate limiter, retries with backoff, and
// automatic cool-down when the server pushes back (429/503).
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"mu-dl/internal/ratelimit"
)

// DefaultUserAgent identifies the tool honestly while staying compatible with
// Cloudflare's browser heuristics (a bare custom token is more likely to be
// challenged than a Mozilla-style string).
const DefaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) MU-Archiver/2.0 (personal archival of subscribed content)"

// ErrPushback is returned (wrapped) when the server signals rate limiting.
var ErrPushback = errors.New("server asked us to slow down")

// Client wraps http.Client with a cookie jar that can be saved to and loaded
// from disk, plus the shared rate limiter.
type Client struct {
	HTTP      *http.Client
	jar       *cookiejar.Jar
	UserAgent string
	Limiter   *ratelimit.Limiter

	// OnPushback is invoked when a 429/503 is seen, with the cool-down chosen.
	OnPushback func(status int, wait time.Duration, url string)

	mu           sync.Mutex
	persistHosts []string
	pushbacks    int
}

// New builds a Client. When followRedirects is false the client returns the
// redirect response itself, which login uses to detect success via the 302.
func New(followRedirects bool, timeout time.Duration, lim *ratelimit.Limiter) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	if lim == nil {
		lim = ratelimit.New(ratelimit.Limits{})
	}
	c := &Client{jar: jar, UserAgent: DefaultUserAgent, Limiter: lim}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 4
	tr.ResponseHeaderTimeout = 60 * time.Second
	c.HTTP = &http.Client{Jar: jar, Timeout: timeout, Transport: tr}
	if !followRedirects {
		c.HTTP.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return c, nil
}

// Jar exposes the cookie jar so a sibling client can share the session.
func (c *Client) Jar() http.CookieJar { return c.jar }

// ShareJar makes this client use another client's cookie jar.
func (c *Client) ShareJar(other *Client) {
	c.jar = other.jar
	c.HTTP.Jar = other.jar
}

// SetPersistHosts records which hosts' cookies should be written by SaveCookies.
func (c *Client) SetPersistHosts(hosts ...string) {
	c.mu.Lock()
	c.persistHosts = hosts
	c.mu.Unlock()
}

// NewRequest builds a request with the standard headers applied.
func (c *Client) NewRequest(ctx context.Context, method, rawurl string, body io.Reader) (*http.Request, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, method, rawurl, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")
	return req, nil
}

// Do executes a request after waiting for the rate limiter. A 429 or 503
// response triggers a global cool-down (honouring Retry-After when present)
// and is returned wrapped in ErrPushback so callers can retry later.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if err := c.Limiter.WaitRequest(req.Context()); err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		wait := c.pushback(resp)
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		if c.OnPushback != nil {
			c.OnPushback(resp.StatusCode, wait, req.URL.String())
		}
		return nil, fmt.Errorf("%w (%s, backing off %s)", ErrPushback, resp.Status, wait.Round(time.Second))
	}
	c.mu.Lock()
	c.pushbacks = 0
	c.mu.Unlock()
	return resp, nil
}

// pushback computes and applies a cool-down for a 429/503 response.
func (c *Client) pushback(resp *http.Response) time.Duration {
	c.mu.Lock()
	c.pushbacks++
	n := c.pushbacks
	c.mu.Unlock()

	wait := 30 * time.Second
	for i := 1; i < n && wait < 15*time.Minute; i++ {
		wait *= 2
	}
	if wait > 15*time.Minute {
		wait = 15 * time.Minute
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil && secs > 0 {
			wait = time.Duration(secs) * time.Second
		} else if t, err := http.ParseTime(ra); err == nil {
			if d := time.Until(t); d > 0 {
				wait = d
			}
		}
	}
	c.Limiter.Cooldown(wait)
	return wait
}

// GetString fetches a URL and returns the body as a string, retrying on
// transient errors (network failures, 5xx, push-back).
func (c *Client) GetString(ctx context.Context, rawurl string) (string, *http.Response, error) {
	body, resp, err := c.GetBytes(ctx, rawurl, nil)
	return string(body), resp, err
}

// GetBytes is GetString with optional extra headers and a []byte body.
func (c *Client) GetBytes(ctx context.Context, rawurl string, headers map[string]string) ([]byte, *http.Response, error) {
	var lastErr error
	backoff := 2 * time.Second
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, backoff); err != nil {
				return nil, nil, err
			}
			backoff *= 2
		}
		req, err := c.NewRequest(ctx, http.MethodGet, rawurl, nil)
		if err != nil {
			return nil, nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := c.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			lastErr = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("server returned %s for %s", resp.Status, rawurl)
			continue
		}
		return body, resp, nil
	}
	return nil, nil, lastErr
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

// --- cookie persistence -------------------------------------------------

type storedCookie struct {
	Host  string `json:"host"`
	Name  string `json:"name"`
	Value string `json:"value"`
	Path  string `json:"path"`
}

// SaveCookies writes the jar's cookies for the persist hosts to path.
func (c *Client) SaveCookies(path string) error {
	c.mu.Lock()
	hosts := append([]string(nil), c.persistHosts...)
	c.mu.Unlock()
	var out []storedCookie
	for _, h := range hosts {
		u := &url.URL{Scheme: "https", Host: h, Path: "/"}
		for _, ck := range c.jar.Cookies(u) {
			out = append(out, storedCookie{Host: h, Name: ck.Name, Value: ck.Value, Path: "/"})
		}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// LoadCookies restores cookies previously written by SaveCookies. A missing
// file is not an error.
func (c *Client) LoadCookies(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var stored []storedCookie
	if err := json.Unmarshal(data, &stored); err != nil {
		return err
	}
	byHost := map[string][]*http.Cookie{}
	for _, sc := range stored {
		byHost[sc.Host] = append(byHost[sc.Host], &http.Cookie{
			Name: sc.Name, Value: sc.Value, Path: "/",
		})
	}
	for h, cks := range byHost {
		u := &url.URL{Scheme: "https", Host: h, Path: "/"}
		c.jar.SetCookies(u, cks)
	}
	return nil
}

// ClearCookies empties the persisted cookie file and the in-memory session
// for the persist hosts.
func (c *Client) ClearCookies(path string) {
	_ = os.Remove(path)
	jar, err := cookiejar.New(nil)
	if err == nil {
		c.jar = jar
		c.HTTP.Jar = jar
	}
}
