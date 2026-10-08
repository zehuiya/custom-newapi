package openai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func responsesStreamErrorTestInfo() *relaycommon.RelayInfo {
	info := streamErrorTestInfo()
	info.RelayFormat = types.RelayFormatOpenAIResponses
	info.RelayMode = relayconstant.RelayModeResponses
	info.StartTime = time.Unix(123, 0)
	return info
}

func responsesStreamErrorTestResponse(sse string) *http.Response {
	response := streamErrorTestResponse(sse)
	response.Header = http.Header{"Content-Type": []string{"text/event-stream"}}
	return response
}

func TestResponsesHandlersRejectUpstreamSSEErrorBeforeOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupStreamErrorTest(t)
	for _, handler := range []struct {
		name string
		run  func(*gin.Context, *relaycommon.RelayInfo, *http.Response) (*dto.Usage, *types.NewAPIError)
	}{
		{name: "stream", run: OaiResponsesStreamHandler},
		{name: "buffered", run: OaiResponsesBufferHandler},
	} {
		for _, testCase := range []struct {
			name       string
			data       string
			statusCode int
			message    string
		}{
			{
				name:       "workbuddy_concurrency_error",
				data:       `{"error":{"type":"rate_limit_error","message":"Concurrency limit exceeded for account, please retry later"}}`,
				statusCode: http.StatusTooManyRequests,
				message:    "Concurrency limit exceeded for account, please retry later",
			},
			{
				name:       "flat_responses_error",
				data:       `{"type":"error","code":"rate_limit_exceeded","message":"Too many requests","param":null}`,
				statusCode: http.StatusTooManyRequests,
				message:    "Too many requests",
			},
			{
				name:       "failed_response",
				data:       `{"type":"response.failed","response":{"id":"resp_failed","status":"failed","error":{"code":"server_error","message":"Provider unavailable"}}}`,
				statusCode: http.StatusBadGateway,
				message:    "Provider unavailable",
			},
			{
				name:       "failed_response_without_error_details",
				data:       `{"type":"response.failed","response":{"id":"resp_failed","status":"failed","error":null}}`,
				statusCode: http.StatusBadGateway,
				message:    "upstream response failed",
			},
			{
				name:       "untyped_error",
				data:       `{"error":{"message":"Provider unavailable"}}`,
				statusCode: http.StatusBadGateway,
				message:    "Provider unavailable",
			},
			{
				name:       "empty_error_event",
				data:       `{"type":"error"}`,
				statusCode: http.StatusBadGateway,
				message:    "upstream stream error",
			},
		} {
			t.Run(handler.name+"/"+testCase.name, func(t *testing.T) {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				info := responsesStreamErrorTestInfo()
				sse := ": PING\n\nevent: error\ndata: " + testCase.data + "\n\ndata: [DONE]\n\n"

				usage, newAPIError := handler.run(c, info, responsesStreamErrorTestResponse(sse))

				require.Nil(t, usage)
				require.NotNil(t, newAPIError)
				require.Equal(t, testCase.statusCode, newAPIError.StatusCode)
				require.Equal(t, testCase.message, newAPIError.Error())
				require.NotEqual(t, types.ErrorCodeBadResponseBody, newAPIError.GetErrorCode())
				require.False(t, types.IsSkipRetryError(newAPIError))
				require.False(t, c.Writer.Written())
				require.Empty(t, w.Body.String())
				require.NotEqual(t, "text/event-stream", w.Header().Get("Content-Type"))
				require.True(t, info.StreamStatus.HasErrors())

				c.JSON(newAPIError.StatusCode, gin.H{"error": newAPIError.ToOpenAIError()})
				require.Equal(t, testCase.statusCode, w.Code)
				require.Contains(t, w.Header().Get("Content-Type"), "application/json")
			})
		}
	}
}

func TestOaiResponsesStreamHandlerRejectsErrorAfterOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupStreamErrorTest(t)
	for _, testCase := range []struct {
		name string
		data string
	}{
		{name: "response_created", data: `{"type":"response.created","response":{"id":"resp_partial","status":"in_progress"}}`},
		{name: "partial_text", data: `{"type":"response.output_text.delta","delta":"partial answer"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			info := responsesStreamErrorTestInfo()
			sse := "data: " + testCase.data + "\n\ndata: {\"error\":{\"type\":\"rate_limit_error\",\"code\":\"concurrency_limit_exceeded\",\"message\":\"account slot timed out\"}}\n\ndata: [DONE]\n\n"

			usage, newAPIError := OaiResponsesStreamHandler(c, info, responsesStreamErrorTestResponse(sse))

			require.Nil(t, usage)
			require.NotNil(t, newAPIError)
			require.Equal(t, http.StatusTooManyRequests, newAPIError.StatusCode)
			require.True(t, types.IsSkipRetryError(newAPIError))
			require.Equal(t, http.StatusOK, w.Code)
			require.Contains(t, w.Body.String(), "event: error\n")
			require.Contains(t, w.Body.String(), `"type":"error"`)
			require.Contains(t, w.Body.String(), `"code":"concurrency_limit_exceeded"`)
			require.Contains(t, w.Body.String(), `"message":"account slot timed out"`)
			require.NotContains(t, w.Body.String(), `"prompt_tokens":52085`)
			require.NotContains(t, w.Body.String(), `"usage"`)
			require.NotContains(t, w.Body.String(), "response.completed")
			require.NotContains(t, w.Body.String(), "[DONE]")
			require.True(t, info.StreamStatus.HasErrors())
		})
	}
}

func TestOaiResponsesStreamHandlerRejectsErrorAfterCommittedPing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupStreamErrorTest(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	_, err := c.Writer.Write([]byte(": PING\n\n"))
	require.NoError(t, err)
	sse := "data: {\"error\":{\"type\":\"rate_limit_error\",\"message\":\"account slot timed out\"}}\n\n"

	usage, newAPIError := OaiResponsesStreamHandler(c, responsesStreamErrorTestInfo(), responsesStreamErrorTestResponse(sse))

	require.Nil(t, usage)
	require.NotNil(t, newAPIError)
	require.True(t, types.IsSkipRetryError(newAPIError))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), ": PING\n\n")
	require.Contains(t, w.Body.String(), "event: error\n")
	require.NotContains(t, w.Body.String(), "[DONE]")
}

func TestOaiResponsesStreamHandlerPreservesNormalUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupStreamErrorTest(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := responsesStreamErrorTestInfo()
	sse := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_ok","status":"in_progress","error":null}}`,
		`data: {"type":"response.output_text.delta","delta":"answer","error":null}`,
		`data: {"type":"response.completed","response":{"id":"resp_ok","status":"completed","error":null,"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}}`,
	}, "\n\n") + "\n\n"

	usage, newAPIError := OaiResponsesStreamHandler(c, info, responsesStreamErrorTestResponse(sse))

	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	require.Equal(t, 7, usage.PromptTokens)
	require.Equal(t, 3, usage.CompletionTokens)
	require.Equal(t, 10, usage.TotalTokens)
	require.Equal(t, "resp_ok", info.UpstreamResponseId)
	require.Contains(t, w.Body.String(), `"created_at":123`)
	require.Contains(t, w.Body.String(), "response.completed")
	require.NotContains(t, w.Body.String(), "event: error\n")
}

func TestOaiResponsesBufferHandlerRejectsErrorAfterBufferedOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial answer\"}\n\ndata: {\"type\":\"error\",\"code\":\"rate_limit_exceeded\",\"message\":\"rate limited\"}\n\n"

	usage, newAPIError := OaiResponsesBufferHandler(c, responsesStreamErrorTestInfo(), responsesStreamErrorTestResponse(sse))

	require.Nil(t, usage)
	require.NotNil(t, newAPIError)
	require.Equal(t, http.StatusTooManyRequests, newAPIError.StatusCode)
	require.False(t, types.IsSkipRetryError(newAPIError))
	require.False(t, c.Writer.Written())
	require.Empty(t, w.Body.String())
}

func TestOaiResponsesBufferHandlerPreservesNormalTerminalResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, eventType := range []string{"response.completed", "response.incomplete"} {
		t.Run(eventType, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			info := responsesStreamErrorTestInfo()
			sse := "data: {\"type\":\"" + eventType + "\",\"response\":{\"id\":\"resp_buffer\",\"error\":null,\"usage\":{\"input_tokens\":7,\"output_tokens\":3,\"total_tokens\":10},\"provider_extension\":{\"preserved\":true}}}\n\n"

			usage, newAPIError := OaiResponsesBufferHandler(c, info, responsesStreamErrorTestResponse(sse))

			require.Nil(t, newAPIError)
			require.NotNil(t, usage)
			require.Equal(t, 7, usage.PromptTokens)
			require.Equal(t, 3, usage.CompletionTokens)
			require.Equal(t, 10, usage.TotalTokens)
			require.Equal(t, "resp_buffer", info.UpstreamResponseId)
			require.Contains(t, w.Header().Get("Content-Type"), "application/json")
			require.Contains(t, w.Body.String(), `"provider_extension":{"preserved":true}`)
			require.NotContains(t, w.Body.String(), "event:")
		})
	}
}

func TestBufferResponsesStreamKeepsMalformedJSONNonRetryable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	buffered, newAPIError := BufferResponsesStream(c, responsesStreamErrorTestInfo(), responsesStreamErrorTestResponse("data: {invalid\n\n"))

	require.Nil(t, buffered)
	require.NotNil(t, newAPIError)
	require.Equal(t, types.ErrorCodeBadResponseBody, newAPIError.GetErrorCode())
}
