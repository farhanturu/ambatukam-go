package ambatukam

import (
	"context"
	"net/http"
)

type Interceptor func(req *http.Request, next PolicyFunc) (*http.Response, error)

type interceptorPolicy struct {
	interceptor Interceptor
}

func (ip *interceptorPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	return ip.interceptor(req, next)
}
