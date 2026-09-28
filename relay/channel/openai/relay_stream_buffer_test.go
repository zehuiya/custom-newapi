package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBufferedChatAccumulatorMergesReasoningContentAndToolCalls(t *testing.T) {
	accumulator := newBufferedChatAccumulator()
	chunks := []string{
		`{"id":"chatcmpl-buffer","object":"chat.completion.chunk","created":123,"model":"mock-model","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"think "},"finish_reason":null}]}`,
		`{"id":"chatcmpl-buffer","object":"chat.completion.chunk","created":123,"model":"mock-model","choices":[{"index":0,"delta":{"reasoning":"carefully","content":"answer ","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-buffer","object":"chat.completion.chunk","created":123,"model":"mock-model","choices":[{"index":0,"delta":{"content":"done","tool_calls":[{"index":0,"function":{"arguments":"\"Guangzhou\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"id":"chatcmpl-buffer","object":"chat.completion.chunk","created":123,"model":"mock-model","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":8,"total_tokens":20}}`,
	}
	for _, chunk := range chunks {
		oaiErr, err := accumulator.merge(chunk)
		require.NoError(t, err)
		require.Nil(t, oaiErr)
	}

	response := accumulator.response("fallback")
	require.Equal(t, "chatcmpl-buffer", response.Id)
	require.Equal(t, "mock-model", response.Model)
	require.Len(t, response.Choices, 1)
	require.Equal(t, "think carefully", response.Choices[0].Message.ReasoningContent)
	require.Equal(t, "answer done", response.Choices[0].Message.StringContent())
	require.Equal(t, "tool_calls", response.Choices[0].FinishReason)

	var toolCalls []dto.ToolCallResponse
	require.NoError(t, common.Unmarshal(response.Choices[0].Message.ToolCalls, &toolCalls))
	require.Len(t, toolCalls, 1)
	require.Equal(t, "call_1", toolCalls[0].ID)
	require.Equal(t, "get_weather", toolCalls[0].Function.Name)
	require.JSONEq(t, `{"city":"Guangzhou"}`, toolCalls[0].Function.Arguments)
	require.True(t, accumulator.hasUsage)
	require.Equal(t, 12, accumulator.usage.PromptTokens)
	require.Equal(t, 8, accumulator.usage.CompletionTokens)
}

func TestBufferedChatAccumulatorRejectsMalformedChunk(t *testing.T) {
	_, err := newBufferedChatAccumulator().merge(`not-json`)
	require.Error(t, err)
}

func TestBufferedChatAccumulatorRequiresTerminalChoiceOnEOF(t *testing.T) {
	accumulator := newBufferedChatAccumulator()
	_, err := accumulator.merge(`{"choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}`)
	require.NoError(t, err)
	require.False(t, accumulator.hasTerminalChoice())

	_, err = accumulator.merge(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	require.NoError(t, err)
	require.True(t, accumulator.hasTerminalChoice())
}

func TestOaiStreamBufferHandlerRejectsPartialEOF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n",
		)),
	}

	_, newAPIError := OaiStreamBufferHandler(ctx, &relaycommon.RelayInfo{}, upstream)
	require.NotNil(t, newAPIError)
	require.Contains(t, newAPIError.Error(), "before a terminal event")
}

func TestOaiStreamBufferHandlerPreservesStructuredUpstreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"error\":{\"message\":\"Concurrency limit exceeded for account, please retry later\",\"type\":\"rate_limit_error\",\"param\":\"\",\"code\":\"concurrency_limit_exceeded\"}}\n\n",
		)),
	}

	usage, newAPIError := OaiStreamBufferHandler(ctx, &relaycommon.RelayInfo{}, upstream)
	require.Nil(t, usage)
	require.NotNil(t, newAPIError)
	require.Equal(t, http.StatusBadGateway, newAPIError.StatusCode)
	require.Equal(t, types.ErrorTypeOpenAIError, newAPIError.GetErrorType())
	require.Equal(t, types.ErrorCode("concurrency_limit_exceeded"), newAPIError.GetErrorCode())
	require.Equal(t, "Concurrency limit exceeded for account, please retry later", newAPIError.Error())
	require.False(t, types.IsSkipRetryError(newAPIError))
	require.False(t, operation_setting.IsAlwaysSkipRetryCode(newAPIError.GetErrorCode()))
	require.True(t, operation_setting.ShouldRetryByStatusCode(newAPIError.StatusCode))
	require.False(t, ctx.Writer.Written())
}

func TestOaiStreamBufferHandlerKeepsMalformedChunkAsBadResponseBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: not-json\n\n")),
	}

	usage, newAPIError := OaiStreamBufferHandler(ctx, &relaycommon.RelayInfo{}, upstream)
	require.Nil(t, usage)
	require.NotNil(t, newAPIError)
	require.Equal(t, http.StatusBadGateway, newAPIError.StatusCode)
	require.Equal(t, types.ErrorCodeBadResponseBody, newAPIError.GetErrorCode())
}
