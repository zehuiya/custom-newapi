package claude

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBufferedClaudeAccumulatorMergesThinkingTextToolAndUsage(t *testing.T) {
	accumulator := newBufferedClaudeAccumulator()
	events := []string{
		`{"type":"message_start","message":{"id":"msg_buffer","type":"message","role":"assistant","model":"mock-claude","usage":{"input_tokens":21,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"think "}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"deeply"}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hello "}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"world"}}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tool_1","name":"lookup","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"value\"}"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":17,"cache_read_input_tokens":5}}`,
		`{"type":"message_stop"}`,
	}
	for index, event := range events {
		done, err := accumulator.merge(event)
		require.NoError(t, err)
		require.Equal(t, index == len(events)-1, done)
	}

	response, err := accumulator.response("fallback")
	require.NoError(t, err)
	require.Equal(t, "msg_buffer", response.Id)
	require.Equal(t, "mock-claude", response.Model)
	require.Equal(t, "tool_use", response.StopReason)
	require.Len(t, response.Content, 3)
	require.Equal(t, "think deeply", *response.Content[0].Thinking)
	require.Equal(t, "hello world", *response.Content[1].Text)
	require.Equal(t, "tool_1", response.Content[2].Id)
	require.Equal(t, "lookup", response.Content[2].Name)
	inputJSON, err := common.Marshal(response.Content[2].Input)
	require.NoError(t, err)
	require.JSONEq(t, `{"q":"value"}`, string(inputJSON))
	require.Equal(t, 21, response.Usage.InputTokens)
	require.Equal(t, 17, response.Usage.OutputTokens)
	require.Equal(t, 5, response.Usage.CacheReadInputTokens)
}

func TestBufferedClaudeAccumulatorRejectsInvalidToolArguments(t *testing.T) {
	accumulator := newBufferedClaudeAccumulator()
	_, err := accumulator.merge(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool_1","name":"lookup","input":{}}}`)
	require.NoError(t, err)
	_, err = accumulator.merge(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{"}}`)
	require.NoError(t, err)
	_, err = accumulator.response("model")
	require.Error(t, err)
}

func TestClaudeStreamBufferHandlerRejectsPartialEOF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_partial","role":"assistant"}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
		}, "\n\n"))),
	}

	_, newAPIError := ClaudeStreamBufferHandler(ctx, upstream, &relaycommon.RelayInfo{})
	require.NotNil(t, newAPIError)
	require.Contains(t, newAPIError.Error(), "before a terminal event")
}
