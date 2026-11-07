package service

import (
	"context"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/logger"
)

func logUpstreamBadRequest(ctx context.Context, header http.Header, body []byte) {
	upstreamID := header.Get("X-Request-Id")
	if upstreamID == "" {
		upstreamID = header.Get("Request-Id")
	}
	if upstreamID == "" {
		upstreamID = header.Get("X-Tt-Logid")
	}
	const maxLoggedBody = 4096
	if len(body) > maxLoggedBody {
		logger.LogError(ctx, fmt.Sprintf("upstream HTTP 400: upstream_request_id=%q body=%q (truncated from %d bytes)", upstreamID, body[:maxLoggedBody], len(body)))
	} else {
		logger.LogError(ctx, fmt.Sprintf("upstream HTTP 400: upstream_request_id=%q body=%q", upstreamID, body))
	}
}
