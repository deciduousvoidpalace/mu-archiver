package archiver

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// fileCache stores feed XML bodies with their validators so repeat scans can
// use conditional requests. It implements feed.Cache.
type fileCache struct {
	dir string
}

type cacheMeta struct {
	URL          string    `json:"url"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	Fetched      time.Time `json:"fetched"`
}

func newFileCache(dir string) *fileCache {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	return &fileCache{dir: dir}
}

func (c *fileCache) key(url string) string {
	h := sha1.Sum([]byte(url))
	return hex.EncodeToString(h[:])
}

func (c *fileCache) Get(url string) ([]byte, string, string, bool) {
	if c == nil {
		return nil, "", "", false
	}
	k := c.key(url)
	body, err := os.ReadFile(filepath.Join(c.dir, k+".xml"))
	if err != nil {
		return nil, "", "", false
	}
	var m cacheMeta
	if data, err := os.ReadFile(filepath.Join(c.dir, k+".json")); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return body, m.ETag, m.LastModified, true
}

func (c *fileCache) Put(url string, body []byte, etag, lastModified string) {
	if c == nil {
		return
	}
	k := c.key(url)
	_ = os.WriteFile(filepath.Join(c.dir, k+".xml"), body, 0o600)
	data, _ := json.Marshal(cacheMeta{URL: url, ETag: etag, LastModified: lastModified, Fetched: time.Now()})
	_ = os.WriteFile(filepath.Join(c.dir, k+".json"), data, 0o600)
}
