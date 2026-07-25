package ambatukam

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

type RequestLogger struct {
	logger     *slog.Logger
	logHeaders []string
	logBody    bool
}

type RequestLogConfig struct {
	Logger     *slog.Logger
	LogHeaders []string
	LogBody    bool
}

func NewRequestLogger(cfg RequestLogConfig) *RequestLogger {
	l := cfg.Logger
	if l == nil {
		l = slog.Default()
	}
	return &RequestLogger{logger: l, logHeaders: cfg.LogHeaders, logBody: cfg.LogBody}
}

func (rl *RequestLogger) Wrap(next PolicyFunc) PolicyFunc {
	return func(ctx context.Context, req *http.Request) (*http.Response, error) {
		start := time.Now()
		attrs := []any{
			slog.String("method", req.Method),
			slog.String("url", req.URL.String()),
		}
		for _, h := range rl.logHeaders {
			if v := req.Header.Get(h); v != "" {
				attrs = append(attrs, slog.String("header."+h, v))
			}
		}
		rl.logger.Debug("request started", attrs...)

		resp, err := next(ctx, req)
		duration := time.Since(start)

		logAttrs := []any{
			slog.String("method", req.Method),
			slog.String("url", req.URL.String()),
			slog.Duration("duration", duration),
		}
		if resp != nil {
			logAttrs = append(logAttrs, slog.Int("status", resp.StatusCode))
		}
		if err != nil {
			logAttrs = append(logAttrs, slog.String("error", err.Error()))
			rl.logger.Warn("request failed", logAttrs...)
		} else {
			rl.logger.Debug("request completed", logAttrs...)
		}
		return resp, err
	}
}
