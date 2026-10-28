package claude

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func handleThinkingSignatureResponse(t *testing.T, info *relaycommon.RelayInfo, data string) ([]byte, *dto.Usage) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	claudeInfo := &ClaudeResponseInfo{ResponseId: "local-generated-id", Usage: &dto.Usage{}}
	err := HandleClaudeResponseData(c, info, claudeInfo, &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
	}, []byte(data))
	require.Nil(t, err)
	return w.Body.Bytes(), claudeInfo.Usage
}

func thinkingSignatureRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeAnthropic,
			UpstreamModelName: "deepseek-flash",
		},
	}
}

func TestHandleClaudeResponseDataFillsMissingThinkingSignaturesAndPreservesFields(t *testing.T) {
	data := `{
		"id":"78420534-e085-4353-a67c-8cac21b259fa",
		"type":"message","role":"assistant","model":"deepseek-flash",
		"content":[
			{"type":"thinking","thinking":"reason one","provider_detail":{"enabled":false,"counter":0}},
			{"type":"thinking","thinking":"reason two","signature":""},
			{"type":"thinking","thinking":"reason three","signature":null},
			{"type":"thinking","thinking":"reason four","signature":"upstream-signature"},
			{"type":"text","text":"answer","citations":[]},
			{"type":"redacted_thinking","data":"opaque-redacted-data"},
			{"type":"tool_use","id":"tool_1","name":"lookup","input":{"flag":false,"amount":0}}
		],
		"stop_reason":"tool_use","stop_sequence":null,
		"usage":{"input_tokens":66,"output_tokens":355,"service_tier":"standard","extra_zero":0},
		"provider_detail":{"enabled":false,"counter":0,"large_integer":9007199254740993}
	}`
	info := thinkingSignatureRelayInfo()
	body, usage := handleThinkingSignatureResponse(t, info, data)
	result := gjson.ParseBytes(body)
	for _, path := range []string{"content.0.signature", "content.1.signature", "content.2.signature"} {
		require.Equal(t, "78420534-e085-4353-a67c-8cac21b259fa", result.Get(path).String(), path)
	}
	require.Equal(t, "upstream-signature", result.Get("content.3.signature").String())
	for _, path := range []string{"content.4.signature", "content.5.signature", "content.6.signature"} {
		require.False(t, result.Get(path).Exists(), path)
	}
	require.Equal(t, "opaque-redacted-data", result.Get("content.5.data").String())
	require.Equal(t, "standard", result.Get("usage.service_tier").String())
	require.Equal(t, "0", result.Get("usage.extra_zero").Raw)
	require.Equal(t, "false", result.Get("content.0.provider_detail.enabled").Raw)
	require.Equal(t, "0", result.Get("content.0.provider_detail.counter").Raw)
	require.Equal(t, "false", result.Get("content.6.input.flag").Raw)
	require.Equal(t, "0", result.Get("content.6.input.amount").Raw)
	require.Equal(t, "false", result.Get("provider_detail.enabled").Raw)
	require.Equal(t, "9007199254740993", result.Get("provider_detail.large_integer").Raw)
	require.Equal(t, "null", result.Get("stop_sequence").Raw)
	require.Equal(t, "[]", result.Get("content.4.citations").Raw)
	require.Equal(t, 66, usage.PromptTokens)
	require.Equal(t, 355, usage.CompletionTokens)
	require.Equal(t, "78420534-e085-4353-a67c-8cac21b259fa", info.UpstreamResponseId)
}

func TestHandleClaudeResponseDataThinkingSignatureGeneratesMissingResponseID(t *testing.T) {
	ids := make(map[string]bool)
	for _, id := range []string{``, `"id":"",`, `"id":null,`} {
		t.Run(id, func(t *testing.T) {
			data := `{` + id + `"type":"message","role":"assistant","model":"deepseek-flash","content":[{"type":"thinking","thinking":"reason"},{"type":"thinking","thinking":"more reason"},{"type":"thinking","thinking":"signed reason","signature":"real-signature"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":3,"service_tier":"standard"},"provider":{"large_integer":9007199254740993,"enabled":false}}`
			body, _ := handleThinkingSignatureResponse(t, thinkingSignatureRelayInfo(), data)
			generatedID := gjson.GetBytes(body, "id").String()
			require.True(t, strings.HasPrefix(generatedID, "msg_"))
			require.Len(t, generatedID, 36)
			require.False(t, ids[generatedID], "separate responses need unique IDs")
			ids[generatedID] = true
			require.Equal(t, generatedID, gjson.GetBytes(body, "content.0.signature").String())
			require.Equal(t, generatedID, gjson.GetBytes(body, "content.1.signature").String())
			require.Equal(t, "real-signature", gjson.GetBytes(body, "content.2.signature").String())
			require.Equal(t, "9007199254740993", gjson.GetBytes(body, "provider.large_integer").Raw)
			require.Equal(t, "false", gjson.GetBytes(body, "provider.enabled").Raw)
			require.Equal(t, "standard", gjson.GetBytes(body, "usage.service_tier").String())
			require.Equal(t, "null", gjson.GetBytes(body, "stop_sequence").Raw)
		})
	}
}

func TestHandleClaudeResponseDataPreservesFullySignedResponse(t *testing.T) {
	data := `{"id":"msg-signed","type":"message","role":"assistant","model":"deepseek-flash","content":[{"type":"thinking","thinking":"reason","signature":"real-signature"},{"type":"text","text":"answer"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":3}}`
	body, _ := handleThinkingSignatureResponse(t, thinkingSignatureRelayInfo(), data)
	require.Equal(t, data, string(body))
}

func TestHandleClaudeResponseDataThinkingSignatureLeavesOtherChannelsUnchanged(t *testing.T) {
	info := thinkingSignatureRelayInfo()
	info.ChannelType = constant.ChannelTypeAws
	data := `{"id":"msg-other-channel","type":"message","role":"assistant","model":"deepseek-flash","content":[{"type":"thinking","thinking":"reason"},{"type":"text","text":"answer"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":3}}`
	body, _ := handleThinkingSignatureResponse(t, info, data)
	require.Equal(t, data, string(body))
}

func TestHandleClaudeResponseDataThinkingSignatureCoexistsWithCacheRewrite(t *testing.T) {
	percentage := 85
	info := thinkingSignatureRelayInfo()
	info.ShouldInjectCacheInfo = true
	info.ChannelSetting = dto.ChannelSettings{
		CacheEnabled:         true,
		CacheOverrideEnabled: true,
		CachePercentageMin:   &percentage,
		CachePercentageMax:   &percentage,
	}
	data := `{"id":"msg-cache-signature","type":"message","role":"assistant","model":"deepseek-flash","content":[{"type":"thinking","thinking":"reason","provider_detail":false},{"type":"text","text":"answer"}],"stop_reason":"end_turn","usage":{"input_tokens":9500,"cache_read_input_tokens":500,"output_tokens":100,"service_tier":"standard"},"provider_detail":{"zero":0}}`
	body, usage := handleThinkingSignatureResponse(t, info, data)
	require.Equal(t, "msg-cache-signature", gjson.GetBytes(body, "content.0.signature").String())
	require.Equal(t, int64(1500), gjson.GetBytes(body, "usage.input_tokens").Int())
	require.Equal(t, int64(8500), gjson.GetBytes(body, "usage.cache_read_input_tokens").Int())
	require.Equal(t, int64(100), gjson.GetBytes(body, "usage.output_tokens").Int())
	require.Equal(t, "standard", gjson.GetBytes(body, "usage.service_tier").String())
	require.Equal(t, "false", gjson.GetBytes(body, "content.0.provider_detail").Raw)
	require.Equal(t, "0", gjson.GetBytes(body, "provider_detail.zero").Raw)
	require.Equal(t, 1500, usage.PromptTokens)
	require.Equal(t, 8500, usage.PromptTokensDetails.CachedTokens)
}

func TestHandleClaudeResponseDataThinkingSignatureLeavesOpenAIConversionUnchanged(t *testing.T) {
	info := thinkingSignatureRelayInfo()
	info.RelayFormat = types.RelayFormatOpenAI
	data := `{"id":"msg-openai","type":"message","role":"assistant","model":"deepseek-flash","content":[{"type":"thinking","thinking":"reason"},{"type":"text","text":"answer"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":3}}`
	body, _ := handleThinkingSignatureResponse(t, info, data)
	require.Equal(t, "msg-openai", gjson.GetBytes(body, "id").String())
	require.Equal(t, "reason", gjson.GetBytes(body, "choices.0.message.reasoning_content").String())
	require.Equal(t, "answer", gjson.GetBytes(body, "choices.0.message.content").String())
	require.False(t, gjson.GetBytes(body, "content").Exists())
	require.NotContains(t, string(body), `"signature"`)
}

func TestClaudeStreamBufferHandlerThinkingSignatureFallback(t *testing.T) {
	for _, tc := range []struct {
		name      string
		deltas    []string
		signature string
		missingID bool
	}{
		{name: "missing signature", signature: "msg-buffer-signature"},
		{name: "missing message id and signature", missingID: true},
		{
			name: "upstream signature fragments",
			deltas: []string{
				`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"upstream-"}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signature"}}`,
			},
			signature: "upstream-signature",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			events := []string{
				`{"type":"message_start","message":{"id":"msg-buffer-signature","type":"message","role":"assistant","model":"deepseek-flash","usage":{"input_tokens":10,"output_tokens":1}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reason"}}`,
			}
			if tc.missingID {
				events[0] = strings.Replace(events[0], `"id":"msg-buffer-signature",`, "", 1)
			}
			events = append(events, tc.deltas...)
			events = append(events,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}`,
				`{"type":"content_block_stop","index":1}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
				`{"type":"message_stop"}`,
			)
			upstream := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: " + strings.Join(events, "\n\ndata: ") + "\n\n")),
			}
			usage, err := ClaudeStreamBufferHandler(c, upstream, thinkingSignatureRelayInfo())
			require.Nil(t, err)
			var response dto.ClaudeResponse
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
			require.Len(t, response.Content, 2)
			if tc.missingID {
				require.True(t, strings.HasPrefix(response.Id, "msg_"))
				require.Len(t, response.Id, 36)
				require.Equal(t, response.Id, response.Content[0].Signature)
			} else {
				require.Equal(t, "msg-buffer-signature", response.Id)
				require.Equal(t, tc.signature, response.Content[0].Signature)
			}
			require.Equal(t, "reason", *response.Content[0].Thinking)
			require.Equal(t, "answer", response.Content[1].GetText())
			require.Empty(t, response.Content[1].Signature)
			require.Equal(t, 10, usage.PromptTokens)
			require.Equal(t, 3, usage.CompletionTokens)
		})
	}
}
