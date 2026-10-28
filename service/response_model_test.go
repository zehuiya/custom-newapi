package service

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func responseModelContext(enabled bool, model string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{RestoreResponseModel: enabled})
	common.SetContextKey(c, constant.ContextKeyOriginalModel, model)
	return c, w
}

func TestRestoreResponseModelProtocolFields(t *testing.T) {
	for _, tc := range []struct {
		name, payload, modelPath string
	}{
		{"chat completion", `{"id":"chat-1","model":"upstream-C","choices":[{"message":{"content":"upstream-C","tool_calls":[{"function":{"arguments":"{\"model\":\"upstream-C\"}"}}]}}],"usage":{"total_tokens":7},"extra":9007199254740993}`, "model"},
		{"completion chunk", `{"object":"chat.completion.chunk","model":"upstream-B","choices":[{"delta":{"content":"hello"}}],"extra":9007199254740993}`, "model"},
		{"responses", `{"object":"response","id":"resp-1","model":"upstream-C","output":[],"extra":9007199254740993}`, "model"},
		{"responses stream", `{"type":"response.created","response":{"id":"resp-1","model":"upstream-C","unknown":false},"extra":9007199254740993}`, "response.model"},
		{"responses terminal event", `{"type":"response.completed","response":{"id":"resp-1","model":"upstream-B","usage":{"total_tokens":7}},"extra":9007199254740993}`, "response.model"},
		{"anthropic", `{"type":"message","id":"msg-1","model":"upstream-C","content":[{"type":"thinking","thinking":"reason","signature":"opaque"}],"extra":9007199254740993}`, "model"},
		{"anthropic stream", `{"type":"message_start","message":{"id":"msg-1","model":"upstream-C","usage":{"input_tokens":3}},"extra":9007199254740993}`, "message.model"},
		{"gemini", `{"modelVersion":"upstream-C","candidates":[],"extra":9007199254740993}`, "modelVersion"},
		{"realtime session", `{"type":"session.created","session":{"id":"session-1","model":"upstream-C"},"extra":9007199254740993}`, "session.model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := responseModelContext(true, "client-A")
			out := RestoreResponseModel(c, []byte(tc.payload))
			require.Equal(t, "client-A", gjson.GetBytes(out, tc.modelPath).String())
			require.Equal(t, "9007199254740993", gjson.GetBytes(out, "extra").Raw)
			require.Equal(t, tc.payload, string(outWithOriginalModel(t, out, tc.modelPath, gjson.Get(tc.payload, tc.modelPath).String())))
		})
	}
}

func outWithOriginalModel(t *testing.T, out []byte, path, model string) []byte {
	t.Helper()
	// Restore only the original model field to compare the untouched payload.
	patched, err := sjson.SetBytes(out, path, model)
	require.NoError(t, err)
	return patched
}

func TestRestoreResponseModelPreservesUnrelatedPayloads(t *testing.T) {
	c, _ := responseModelContext(true, "client-A")
	for _, payload := range []string{
		`[DONE]`, `not json`, "\x00\x01\xff", `[]`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"{\"model\":\"C\"}"}}`,
		`{"type":"response.output_text.delta","delta":"upstream-C"}`,
		`{"type":"error","model":"upstream-C","error":{"message":"upstream-C unavailable"}}`,
		`{"model":"upstream-C","error":{"message":"unavailable"}}`,
		`{"model":null,"vendor":{"model":"upstream-C"}}`,
		`{"type":"custom_event","message":{"model":"upstream-C"},"response":{"model":"upstream-C"}}`,
	} {
		require.Equal(t, payload, string(RestoreResponseModel(c, []byte(payload))))
	}
}

func TestRestoreResponseModelDefaultAndChannelRetry(t *testing.T) {
	payload := []byte(`{"model":"upstream-C","choices":[]}`)
	c, _ := responseModelContext(false, "client-A")
	require.Equal(t, payload, RestoreResponseModel(c, payload))
	require.Equal(t, payload, RestoreResponseModel(nil, payload))
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{RestoreResponseModel: true})
	require.Equal(t, "client-A", gjson.GetBytes(RestoreResponseModel(c, payload), "model").String())
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{})
	require.Equal(t, payload, RestoreResponseModel(c, payload), "retry onto an unchecked channel must preserve its model")
	c, _ = responseModelContext(true, "")
	require.Equal(t, payload, RestoreResponseModel(c, payload))
}

func TestIOCopyBytesGracefullyRestoresModelAndContentLength(t *testing.T) {
	c, w := responseModelContext(true, "client-A-with-a-long-name")
	src := &http.Response{StatusCode: http.StatusOK, Header: http.Header{
		"Content-Type": {"application/json"}, "Content-Length": {"1"}, "X-Trace-Id": {"keep"},
	}}
	payload := []byte(`{"id":"msg-1","model":"C","content":[{"type":"thinking","signature":"msg-1"}],"usage":{"input_tokens":3,"output_tokens":2}}`)
	IOCopyBytesGracefully(c, src, payload)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "client-A-with-a-long-name", gjson.GetBytes(w.Body.Bytes(), "model").String())
	require.Equal(t, "msg-1", gjson.GetBytes(w.Body.Bytes(), "content.0.signature").String())
	require.Equal(t, "keep", w.Header().Get("X-Trace-Id"))
	require.Equal(t, strconv.Itoa(w.Body.Len()), w.Header().Get("Content-Length"))

	c, w = responseModelContext(true, "client-A")
	src.StatusCode = http.StatusBadRequest
	IOCopyBytesGracefully(c, src, payload)
	require.Equal(t, string(payload), w.Body.String(), "HTTP errors must remain unchanged")
}
