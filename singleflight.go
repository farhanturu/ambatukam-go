package ambatukam

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type SingleflightPolicy struct {
	calls       map[string]*singleflightCall
	mu          sync.Mutex
	maxBodySize int64
}

type singleflightCall struct {
	val       *http.Response
	err       error
	created   time.Time
	bodyBytes []byte
	wg        sync.WaitGroup
}

func NewSingleflight() *SingleflightPolicy {
	return &SingleflightPolicy{calls: make(map[string]*singleflightCall)}
}

func (sf *SingleflightPolicy) SetMaxBodySize(n int64) {
	sf.maxBodySize = n
}

func (sf *SingleflightPolicy) buildKey(req *http.Request) (string, error) {
	base := req.Method + " " + req.URL.String()
	if req.Body == nil || req.Method == http.MethodGet || req.Method == http.MethodHead ||
		req.Method == http.MethodOptions || req.Method == http.MethodDelete {
		return base, nil
	}
	if sf.maxBodySize > 0 {
		limited := io.LimitReader(req.Body, sf.maxBodySize+1)
		bodyBytes, err := io.ReadAll(limited)
		if err != nil {
			return "", err
		}
		_ = req.Body.Close()
		if int64(len(bodyBytes)) > sf.maxBodySize {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			req.ContentLength = int64(len(bodyBytes))
			return base, nil
		}
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(bodyBytes)), nil
		}
		req.ContentLength = int64(len(bodyBytes))
		h := sha256.Sum256(bodyBytes)
		return base + " body:" + hex.EncodeToString(h[:8]), nil
	}
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return "", err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyBytes)), nil
	}
	req.ContentLength = int64(len(bodyBytes))
	h := sha256.Sum256(bodyBytes)
	return base + " body:" + hex.EncodeToString(h[:8]), nil
}

// singleflightTTL is the max age of a call entry before it is treated as stale.
const singleflightTTL = 60 * time.Second

func (sf *SingleflightPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	key, err := sf.buildKey(req)
	if err != nil {
		return nil, err
	}
	sf.mu.Lock()
	if c, ok := sf.calls[key]; ok {
		// Evict stale entries that outlived the TTL.
		if time.Since(c.created) > singleflightTTL {
			delete(sf.calls, key)
		} else {
			sf.mu.Unlock()
			c.wg.Wait()
			if c.err != nil {
				return nil, c.err
			}
			clone := *c.val
			clone.Body = io.NopCloser(bytes.NewReader(c.bodyBytes))
			clone.ContentLength = int64(len(c.bodyBytes))
			return &clone, nil
		}
	}
	c := &singleflightCall{created: time.Now()}
	c.wg.Add(1)
	sf.calls[key] = c
	sf.mu.Unlock()

	// Panic-safe: always mark the call done so waiters don't deadlock.
	func() {
		defer func() {
			if r := recover(); r != nil {
				c.err = fmt.Errorf("ambatukam: singleflight panic: %v", r)
				c.wg.Done()
			}
		}()
		c.val, c.err = next(ctx, req)
	}()

	if c.val != nil && c.val.Body != nil && c.err == nil {
		c.bodyBytes, _ = io.ReadAll(c.val.Body)
		c.val.Body.Close()
		c.val.Body = io.NopCloser(bytes.NewReader(c.bodyBytes))
		c.val.ContentLength = int64(len(c.bodyBytes))
	}
	c.wg.Done()

	sf.mu.Lock()
	delete(sf.calls, key)
	sf.mu.Unlock()

	if c.err != nil {
		return nil, c.err
	}
	clone := *c.val
	clone.Body = io.NopCloser(bytes.NewReader(c.bodyBytes))
	clone.ContentLength = int64(len(c.bodyBytes))
	return &clone, nil
}
