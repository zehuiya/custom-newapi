package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRewriteRelayResponseID(t *testing.T) {
	const requestID = "202611061234567890123456268d9d6testID00"
	for _, tc := range []struct{ name, path, body, idPath string }{
		{"chat", "/v1/chat/completions", `{"id":"upstream","choices":[{"message":{"content":"id: upstream","tool_calls":[{"id":"call_keep","function":{"arguments":"{\"id\":\"keep\"}"}}]}}],"extra":9007199254740993}`, "id"},
		{"chat chunk", "/v1/chat/completions", `{"id":"upstream","object":"chat.completion.chunk","choices":[],"extra":9007199254740993}`, "id"},
		{"chat missing id", "/v1/chat/completions", `{"choices":[],"extra":9007199254740993}`, "id"},
		{"claude", "/v1/messages", `{"id":"msg_upstream","type":"message","content":[{"type":"tool_use","id":"tool_keep"},{"type":"thinking","signature":"sig_keep"}],"extra":9007199254740993}`, "id"},
		{"claude start", "/v1/messages", `{"type":"message_start","message":{"id":"msg_upstream","content":[]},"extra":9007199254740993}`, "message.id"},
		{"playground", "/pg/chat/completions", `{"id":"upstream","choices":[],"extra":9007199254740993}`, "id"},
	} {
		for _, modelEnabled := range []bool{false, true} {
			t.Run(tc.name, func(t *testing.T) {
				c, _ := responseModelContext(modelEnabled, "client-model")
				c.Request = httptest.NewRequest("POST", tc.path, nil)
				c.Set(common.RequestIdKey, requestID)
				out := RewriteRelayResponse(c, []byte(tc.body))
				require.Equal(t, requestID, gjson.GetBytes(out, tc.idPath).String())
				require.Equal(t, "9007199254740993", gjson.GetBytes(out, "extra").Raw)
				require.Equal(t, string(out), RewriteRelayResponseString(c, tc.body))
				for _, path := range []string{"choices", "content", "message.content"} {
					require.Equal(t, gjson.Get(tc.body, path).Raw, gjson.GetBytes(out, path).Raw)
				}
			})
		}
	}
}

func TestRewriteRelayResponseIDPreservesProtocolReferences(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/v1/responses", `{"id":"resp_keep","object":"response","output":[{"id":"msg_keep","call_id":"call_keep"}]}`},
		{"/v1/responses", `{"type":"response.completed","response":{"id":"resp_keep"}}`},
		{"/v1/responses/compact", `{"id":"resp_keep","object":"response.compaction"}`},
		{"/v1/images/generations", `{"id":"image_keep"}`},
		{"/v1/messages", `{"type":"content_block_start","content_block":{"id":"tool_keep","type":"tool_use"}}`},
		{"/v1/messages", `{"type":"content_block_delta","delta":{"type":"signature_delta","signature":"keep"}}`},
		{"/v1/messages", `{"type":"message_stop"}`},
		{"/v1/chat/completions", `{"id":"error_keep","error":{"message":"unavailable"}}`},
		{"/v1/messages", `{"type":"error","error":{"message":"unavailable"}}`},
		{"/v1/chat/completions", `[DONE]`},
		{"/v1/chat/completions", `{invalid`},
	} {
		c, _ := responseModelContext(false, "")
		c.Request = httptest.NewRequest("POST", tc.path, nil)
		c.Set(common.RequestIdKey, "local-request-id")
		require.Equal(t, tc.body, string(RewriteRelayResponse(c, []byte(tc.body))))
	}
	c, _ := responseModelContext(false, "")
	require.Equal(t, `{"id":"keep"}`, string(RewriteRelayResponse(c, []byte(`{"id":"keep"}`))))
	require.Equal(t, `{"id":"keep"}`, RewriteRelayResponseString((*gin.Context)(nil), `{"id":"keep"}`))
}

func TestRewriteRelayResponseIDPreservesRequestHeader(t *testing.T) {
	c, w := responseModelContext(false, "")
	c.Set(common.RequestIdKey, "local-request-id")
	src := httptest.NewRecorder()
	src.Header().Set(common.RequestIdKey, "upstream-request-id")
	src.WriteHeader(200)
	IOCopyBytesGracefully(c, src.Result(), []byte(`{"id":"upstream","choices":[]}`))
	require.Equal(t, "local-request-id", w.Header().Get(common.RequestIdKey))
	require.Equal(t, "local-request-id", gjson.Get(w.Body.String(), "id").String())
}
