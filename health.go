package ambatukam

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"
)

type HealthStatus struct {
	Status    string            `json:"status"`
	Timestamp time.Time         `json:"timestamp"`
	Uptime    time.Duration     `json:"uptime"`
	Policies  map[string]string `json:"policies"`
	Memory    MemoryStats       `json:"memory"`
}

type MemoryStats struct {
	Alloc      uint64 `json:"alloc_bytes"`
	TotalAlloc uint64 `json:"total_alloc_bytes"`
	Sys        uint64 `json:"sys_bytes"`
	NumGC      uint32 `json:"num_gc"`
}

type HealthChecker struct {
	client  *Client
	started time.Time
}

func NewHealthChecker(c *Client) *HealthChecker {
	return &HealthChecker{client: c, started: time.Now()}
}

func (h *HealthChecker) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := h.getStatus()
		w.Header().Set("Content-Type", "application/json")
		if status.Status != "healthy" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(status)
	}
}

func (h *HealthChecker) getStatus() HealthStatus {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	policies := make(map[string]string)
	for _, p := range h.client.policies {
		switch pp := p.(type) {
		case *CircuitBreakerPolicy:
			policies["circuit_breaker"] = string(pp.State())
		case *BulkheadPolicy:
			policies["bulkhead_in_flight"] = formatUint32(pp.InFlight())
			policies["bulkhead_denied"] = formatUint64(pp.Denied())
		}
	}

	status := "healthy"
	for _, v := range policies {
		if v == "open" {
			status = "degraded"
			break
		}
	}

	return HealthStatus{
		Status:    status,
		Timestamp: time.Now(),
		Uptime:    time.Since(h.started),
		Policies:  policies,
		Memory: MemoryStats{
			Alloc:      m.Alloc,
			TotalAlloc: m.TotalAlloc,
			Sys:        m.Sys,
			NumGC:      m.NumGC,
		},
	}
}

func formatUint32(v uint32) string {
	return string(rune(v + 48))
}

func formatUint64(v uint64) string {
	return string(rune(v + 48))
}
