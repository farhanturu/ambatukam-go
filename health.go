package ambatukam

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

type HealthStatus struct {
	Timestamp time.Time         `json:"timestamp"`
	Policies  map[string]string `json:"policies"`
	Status    string            `json:"status"`
	Memory    MemoryStats       `json:"memory"`
	Uptime    time.Duration     `json:"uptime"`
}

type MemoryStats struct {
	Alloc      uint64 `json:"alloc_bytes"`
	TotalAlloc uint64 `json:"total_alloc_bytes"`
	Sys        uint64 `json:"sys_bytes"`
	NumGC      uint32 `json:"num_gc"`
}

type HealthChecker struct {
	client     *Client
	started    time.Time
	memStats   atomic.Pointer[runtime.MemStats]
	stopCh     chan struct{}
	memUpdated atomic.Int64
}

func NewHealthChecker(c *Client) *HealthChecker {
	h := &HealthChecker{client: c, started: time.Now(), stopCh: make(chan struct{})}
	h.refreshMemStats()
	go h.refreshMemStatsLoop()
	return h
}

func (h *HealthChecker) refreshMemStats() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	h.memStats.Store(&m)
	h.memUpdated.Store(time.Now().UnixNano())
}

func (h *HealthChecker) refreshMemStatsLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			h.refreshMemStats()
		}
	}
}

func (h *HealthChecker) Close() {
	close(h.stopCh)
}

func (h *HealthChecker) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := h.getStatus()
		w.Header().Set("Content-Type", "application/json")
		if status.Status != "healthy" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(status)
	}
}

func (h *HealthChecker) getStatus() HealthStatus {
	m := h.memStats.Load()
	policies := make(map[string]string)
	for _, p := range h.client.policies {
		switch pp := p.(type) {
		case *CircuitBreakerPolicy:
			policies["circuit_breaker"] = string(pp.State())
		case *BulkheadPolicy:
			policies["bulkhead_in_flight"] = strconv.FormatUint(uint64(pp.InFlight()), 10)
			policies["bulkhead_denied"] = strconv.FormatUint(uint64(pp.Denied()), 10)
		}
	}
	status := "healthy"
	for _, v := range policies {
		if v == "open" {
			status = "degraded"
			break
		}
	}
	var memStats MemoryStats
	if m != nil {
		memStats = MemoryStats{
			Alloc:      m.Alloc,
			TotalAlloc: m.TotalAlloc,
			Sys:        m.Sys,
			NumGC:      m.NumGC,
		}
	}
	return HealthStatus{
		Status:    status,
		Timestamp: time.Now(),
		Uptime:    time.Since(h.started),
		Policies:  policies,
		Memory:    memStats,
	}
}
