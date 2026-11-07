package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/logger"
)

func logVolcengineBadRequest(ctx context.Context, resp *http.Response, body []byte) bool {
	if resp.StatusCode != http.StatusBadRequest || resp.Request == nil || resp.Request.URL == nil {
		return false
	}
	host := strings.ToLower(resp.Request.URL.Hostname())
	if !strings.HasPrefix(host, "ark.") || !strings.HasSuffix(host, ".volces.com") {
		return false
	}
	upstreamID := resp.Header.Get("X-Request-Id")
	if upstreamID == "" {
		upstreamID = resp.Header.Get("X-Tt-Logid")
	}
	const maxLoggedBody = 4096
	if len(body) > maxLoggedBody {
		logger.LogError(ctx, fmt.Sprintf("volcengine upstream 400: upstream_request_id=%q body=%q (truncated from %d bytes)", upstreamID, body[:maxLoggedBody], len(body)))
	} else {
		logger.LogError(ctx, fmt.Sprintf("volcengine upstream 400: upstream_request_id=%q body=%q", upstreamID, body))
	}
	return true
}
