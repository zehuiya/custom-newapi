package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponsesSSEErrorRetryContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	for _, testCase := range []struct {
		name        string
		prefix      string
		shouldRetry bool
	}{
		{name: "upstream_ping_only", prefix: ": PING\n\n", shouldRetry: true},
		{name: "downstream_response_started", prefix: "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\"}}\n\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			info := &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatOpenAIResponses,
				RelayMode:   relayconstant.RelayModeResponses,
				DisablePing: true,
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "test-model"},
			}
			sse := testCase.prefix + "event: error\ndata: {\"error\":{\"type\":\"rate_limit_error\",\"message\":\"Concurrency limit exceeded for account, please retry later\"}}\n\n"
			response := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(sse)),
			}

			usage, newAPIError := openai.OaiResponsesStreamHandler(c, info, response)

			require.Nil(t, usage)
			require.NotNil(t, newAPIError)
			require.Equal(t, http.StatusTooManyRequests, newAPIError.StatusCode)
			require.Equal(t, testCase.shouldRetry, shouldRetry(c, newAPIError, 1))
			if !testCase.shouldRetry {
				return
			}

			// The next attempt must be able to reuse the uncommitted response.
			response.Body = io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_retry\",\"status\":\"completed\",\"usage\":{\"input_tokens\":7,\"output_tokens\":3,\"total_tokens\":10}}}\n\n"))
			usage, newAPIError = openai.OaiResponsesStreamHandler(c, info, response)

			require.Nil(t, newAPIError)
			require.NotNil(t, usage)
			require.Equal(t, 10, usage.TotalTokens)
			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
			require.Contains(t, w.Body.String(), "resp_retry")
			require.NotContains(t, w.Body.String(), "Concurrency limit exceeded")
		})
	}
}
