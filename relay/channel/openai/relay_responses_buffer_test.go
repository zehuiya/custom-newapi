package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBufferResponsesStreamExtractsTerminalResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	sse := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_buffer","object":"response","status":"in_progress"}}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_buffer","object":"response","created_at":123,"status":"completed","model":"mock-model","output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10},"provider_extension":{"preserved":true}}}`,
		"",
	}, "\n")
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}
	info := &relaycommon.RelayInfo{}

	buffered, newAPIError := BufferResponsesStream(ctx, info, upstream)
	require.Nil(t, newAPIError)
	require.Equal(t, "application/json", buffered.Header.Get("Content-Type"))
	body, err := io.ReadAll(buffered.Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"resp_buffer","object":"response","created_at":123,"status":"completed","model":"mock-model","output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10},"provider_extension":{"preserved":true}}`, string(body))
}

func TestBufferResponsesStreamRequiresTerminalResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")),
	}

	_, newAPIError := BufferResponsesStream(ctx, &relaycommon.RelayInfo{}, upstream)
	require.NotNil(t, newAPIError)
	require.Contains(t, newAPIError.Error(), "terminal response")
}
