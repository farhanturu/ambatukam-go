package ambatukam

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

type cacheEntry struct {
	resp      *http.Response
	body      []byte
	createdAt time.Time
}

type CacheConfig struct {
	TTL        time.Duration
	MaxEntries int
	Methods    []string
}

type CachePolicy struct {
	entries map[string]*cacheEntry
	mu      sync.RWMutex
	cfg     CacheConfig
	stats   *statsRecorder
}

func NewCache(cfg CacheConfig) *CachePolicy {
	if cfg.TTL == 0 {
		cfg.TTL = 5 * time.Minute
	}
	if cfg.MaxEntries == 0 {
		cfg.MaxEntries = 1000
	}
	if len(cfg.Methods) == 0 {
		cfg.Methods = []string{http.MethodGet, http.MethodHead}
	}
	return &CachePolicy{
		entries: make(map[string]*cacheEntry, cfg.MaxEntries),
		cfg:     cfg,
	}
}

func (c *CachePolicy) SetStats(s *statsRecorder) {
	c.stats = s
}

func (c *CachePolicy) isCacheable(method string) bool {
	for _, m := range c.cfg.Methods {
		if m == method {
			return true
		}
	}
	return false
}

func (c *CachePolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	if !c.isCacheable(req.Method) {
		return next(ctx, req)
	}

	key := req.Method + " " + req.URL.String()

	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()

	if ok && time.Since(entry.createdAt) < c.cfg.TTL {
		if c.stats != nil {
			c.stats.cacheHits.Add(1)
		}
		clone := *entry.resp
		clone.Body = io.NopCloser(bytes.NewReader(entry.body))
		clone.ContentLength = int64(len(entry.body))
		return &clone, nil
	}

	if c.stats != nil {
		c.stats.cacheMisses.Add(1)
	}

	resp, err := next(ctx, req)
	if err != nil || resp == nil {
		return resp, err
	}

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))

	c.mu.Lock()
	if len(c.entries) >= c.cfg.MaxEntries {
		oldest := ""
		oldestTime := time.Now()
		for k, v := range c.entries {
			if v.createdAt.Before(oldestTime) {
				oldest = k
				oldestTime = v.createdAt
			}
		}
		if oldest != "" {
			delete(c.entries, oldest)
		}
	}
	c.entries[key] = &cacheEntry{resp: resp, body: body, createdAt: time.Now()}
	c.mu.Unlock()

	return resp, nil
}
