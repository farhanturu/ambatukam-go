package ambatukam

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"sync"
)

type SingleflightPolicy struct {
	mu    sync.Mutex
	calls map[string]*singleflightCall
}

type singleflightCall struct {
	wg        sync.WaitGroup
	val       *http.Response
	bodyBytes []byte
	err       error
}

func NewSingleflight() *SingleflightPolicy {
	return &SingleflightPolicy{calls: make(map[string]*singleflightCall)}
}

func (sf *SingleflightPolicy) buildKey(req *http.Request) (string, error) {
	base := req.Method + " " + req.URL.String()
	if req.Body == nil || req.Method == http.MethodGet || req.Method == http.MethodHead ||
		req.Method == http.MethodOptions || req.Method == http.MethodDelete {
		return base, nil
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

func (sf *SingleflightPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	key, err := sf.buildKey(req)
	if err != nil {
		return nil, err
	}
	sf.mu.Lock()
	if c, ok := sf.calls[key]; ok {
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
	c := &singleflightCall{}
	c.wg.Add(1)
	sf.calls[key] = c
	sf.mu.Unlock()

	c.val, c.err = next(ctx, req)

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
