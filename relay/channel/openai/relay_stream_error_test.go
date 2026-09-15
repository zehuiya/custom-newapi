package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func streamErrorTestInfo() *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		DisablePing: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "test-model"},
	}
	info.SetEstimatePromptTokens(52085)
	return info
}

func streamErrorTestResponse(sse string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse))}
}

func setupStreamErrorTest(t *testing.T) {
	t.Helper()
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() {
		constant.StreamingTimeout = oldStreamingTimeout
	})
}

func TestOaiStreamHandlerRejectsUpstreamSSEErrorBeforeOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupStreamErrorTest(t)
	for _, testCase := range []struct {
		name string
		sse  string
	}{
		{
			name: "explicit_error_event",
			sse:  "event: error\ndata: {\"error\":{\"type\":\"rate_limit_error\",\"message\":\"Concurrency limit exceeded for account, please retry later\"}}\n\n",
		},
		{
			name: "error_data_without_event_line",
			sse:  "data: {\"error\":{\"type\":\"rate_limit_error\",\"message\":\"Concurrency limit exceeded for account, please retry later\"}}\n\n",
		},
		{
			name: "error_without_type",
			sse:  "data: {\"error\":{\"message\":\"upstream unavailable\"}}\n\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := streamErrorTestInfo()

			usage, newAPIError := OaiStreamHandler(c, info, streamErrorTestResponse(testCase.sse))

			require.Nil(t, usage)
			require.NotNil(t, newAPIError)
			require.Equal(t, http.StatusBadGateway, newAPIError.StatusCode)
			require.False(t, types.IsSkipRetryError(newAPIError))
			require.False(t, c.Writer.Written())
			require.Empty(t, w.Body.String())
			require.NotEqual(t, "text/event-stream", w.Header().Get("Content-Type"))
			require.True(t, info.StreamStatus.HasErrors())

			c.JSON(newAPIError.StatusCode, gin.H{"error": newAPIError.ToOpenAIError()})
			require.Equal(t, http.StatusBadGateway, w.Code)
			require.Contains(t, w.Header().Get("Content-Type"), "application/json")
		})
	}
}

func TestOaiStreamHandlerSendsErrorAfterPartialOutputWithoutUsageOrDone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupStreamErrorTest(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	sse := strings.Join([]string{
		"data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"first\"},\"finish_reason\":null}]}",
		"data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"second\"},\"finish_reason\":null}]}",
		"event: error\ndata: {\"error\":{\"type\":\"rate_limit_error\",\"message\":\"account slot timed out\"}}",
	}, "\n\n")
	info := streamErrorTestInfo()

	usage, newAPIError := OaiStreamHandler(c, info, streamErrorTestResponse(sse))

	require.Nil(t, usage)
	require.NotNil(t, newAPIError)
	require.True(t, types.IsSkipRetryError(newAPIError))
	require.True(t, c.Writer.Written())
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "\"content\":\"first\"")
	require.Contains(t, w.Body.String(), "event: error\ndata: ")
	require.Contains(t, w.Body.String(), "\"message\":\"account slot timed out\"")
	require.NotContains(t, w.Body.String(), "\"prompt_tokens\":52085")
	require.NotContains(t, w.Body.String(), "[DONE]")
}

func TestOaiStreamHandlerSendsErrorAfterOnlyPingCommittedStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupStreamErrorTest(t)
	for _, relayFormat := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		_, err := c.Writer.Write([]byte(": PING\n\n"))
		require.NoError(t, err)
		info := streamErrorTestInfo()
		info.RelayFormat = relayFormat
		sse := "event: error\ndata: {\"error\":{\"type\":\"rate_limit_error\",\"message\":\"account slot timed out\"}}\n\n"

		usage, newAPIError := OaiStreamHandler(c, info, streamErrorTestResponse(sse))

		require.Nil(t, usage)
		require.NotNil(t, newAPIError)
		require.True(t, types.IsSkipRetryError(newAPIError))
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), ": PING\n\n")
		require.Contains(t, w.Body.String(), "event: error\ndata: ")
		require.Contains(t, w.Body.String(), "\"message\":\"account slot timed out\"")
		if relayFormat == types.RelayFormatClaude {
			require.Contains(t, w.Body.String(), "\"type\":\"error\"")
		}
		require.NotContains(t, w.Body.String(), "\"prompt_tokens\":52085")
		require.NotContains(t, w.Body.String(), "[DONE]")
	}
}

func TestOaiStreamHandlerAcceptsNullErrorInNormalStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupStreamErrorTest(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	sse := strings.Join([]string{
		"data: {\"error\":null,\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}",
		"data: [DONE]",
	}, "\n\n")
	info := streamErrorTestInfo()

	usage, newAPIError := OaiStreamHandler(c, info, streamErrorTestResponse(sse))

	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	require.Contains(t, w.Body.String(), "\"content\":\"answer\"")
	require.Contains(t, w.Body.String(), "[DONE]")
}

func TestOpenAIStreamErrorIgnoresAbsentAndNullErrorFields(t *testing.T) {
	require.Nil(t, openAIStreamError("{\"choices\":[],\"usage\":{\"prompt_tokens\":12}}"))
	require.Nil(t, openAIStreamError("{\"error\":null,\"choices\":[]}"))
	require.Nil(t, openAIStreamError("{\"choices\":[{\"delta\":{\"content\":\"error\"}}]}"))
}

func TestOpenAIStreamErrorPreservesStructuredCode(t *testing.T) {
	newAPIError := openAIStreamError("{\"error\":{\"type\":\"rate_limit_error\",\"message\":\"slot full\",\"code\":\"concurrency_limit_exceeded\"}}")
	require.NotNil(t, newAPIError)
	require.Equal(t, types.ErrorCode("concurrency_limit_exceeded"), newAPIError.GetErrorCode())
	require.Equal(t, http.StatusBadGateway, newAPIError.StatusCode)
	require.Equal(t, "slot full", newAPIError.Error())
}
