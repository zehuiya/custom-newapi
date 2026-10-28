package claude

import (
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

func TestHandleStreamResponseDataMissingThinkingSignature(t *testing.T) {
	start := `{"type":"message_start","message":{"id":"msg-upstream","role":"assistant","model":"deepseek-flash"},"vendor_meta":{"keep":true}}`
	thinkingStart := `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`
	thinkingDelta := `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reasoning"},"vendor_delta":true}`
	blockStop := `{"type":"content_block_stop","index":0}`
	messageStop := `{"type":"message_stop"}`

	tests := []struct {
		name     string
		events   []string
		injected map[int]string
	}{
		{
			name:     "missing signature at index zero",
			events:   []string{start, thinkingStart, thinkingDelta, blockStop, messageStop},
			injected: map[int]string{0: "msg-upstream"},
		},
		{
			name: "empty upstream signatures",
			events: []string{start,
				`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
				thinkingDelta,
				`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":""}}`,
				blockStop, messageStop},
			injected: map[int]string{0: "msg-upstream"},
		},
		{
			name: "preserve signature in block start",
			events: []string{start,
				`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":"upstream-signature"}}`,
				thinkingDelta, blockStop, messageStop},
		},
		{
			name: "preserve fragmented signature deltas",
			events: []string{start, thinkingStart, thinkingDelta,
				`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"first"}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"second"}}`,
				blockStop, messageStop},
		},
		{
			name: "track interleaved blocks independently",
			events: []string{start, thinkingStart,
				`{"type":"content_block_start","index":2,"content_block":{"type":"thinking","thinking":"other"}}`,
				`{"type":"content_block_start","index":4,"content_block":{"type":"thinking","thinking":"signed"}}`,
				`{"type":"content_block_delta","index":4,"delta":{"type":"signature_delta","signature":"real-signature"}}`,
				thinkingDelta,
				`{"type":"content_block_stop","index":2}`, blockStop,
				`{"type":"content_block_stop","index":4}`, messageStop},
			injected: map[int]string{0: "msg-upstream", 2: "msg-upstream"},
		},
		{
			name: "text tool and redacted blocks unchanged",
			events: []string{start,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"answer"}}`, blockStop,
				`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tool-1","name":"lookup","input":{}}}`,
				`{"type":"content_block_stop","index":1}`,
				`{"type":"content_block_start","index":2,"content_block":{"type":"redacted_thinking","data":"opaque"}}`,
				`{"type":"content_block_stop","index":2}`, messageStop},
		},
		{
			name:   "no message start",
			events: []string{thinkingStart, thinkingDelta, blockStop, messageStop},
		},
		{
			name: "escaped upstream id",
			events: []string{`{"type":"message_start","message":{"id":"msg-\"quoted\"-\\-\n","role":"assistant"}}`,
				thinkingStart, thinkingDelta, blockStop, messageStop},
			injected: map[int]string{0: "msg-\"quoted\"-\\-\n"},
		},
		{
			name:     "duplicate block stop does not inject twice",
			events:   []string{start, thinkingStart, thinkingDelta, blockStop, blockStop, messageStop},
			injected: map[int]string{0: "msg-upstream"},
		},
		{
			name: "new message resets pending blocks",
			events: []string{start, thinkingStart,
				`{"type":"message_start","message":{"id":"another-message","role":"assistant"}}`,
				blockStop, messageStop},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatClaude,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAnthropic},
			}
			output := runThinkingSignatureStream(t, info, tt.events)
			originalIndex := 0
			injected := map[int]string{}
			for i, eventData := range output {
				if originalIndex < len(tt.events) && eventData == tt.events[originalIndex] {
					originalIndex++
					continue
				}
				var event dto.ClaudeResponse
				require.NoError(t, common.UnmarshalJsonStr(eventData, &event))
				require.Equal(t, "content_block_delta", event.Type)
				require.NotNil(t, event.Index, "zero index must be explicit")
				require.NotNil(t, event.Delta)
				require.Equal(t, "signature_delta", event.Delta.Type)
				require.NotContains(t, injected, event.GetIndex(), "one fallback per block")
				injected[event.GetIndex()] = event.Delta.Signature
				require.Less(t, i+1, len(output))
				var next dto.ClaudeResponse
				require.NoError(t, common.UnmarshalJsonStr(output[i+1], &next))
				require.Equal(t, "content_block_stop", next.Type)
				require.Equal(t, event.GetIndex(), next.GetIndex())
			}
			require.Equal(t, len(tt.events), originalIndex, "upstream events must remain unchanged")
			if len(tt.injected) == 0 {
				require.Empty(t, injected)
			} else {
				require.Equal(t, tt.injected, injected)
			}
		})
	}
}

func TestHandleStreamResponseDataGeneratesMissingIDForThinkingSignatures(t *testing.T) {
	for _, idField := range []string{"", `"id":"",`, `"id":null,`} {
		t.Run(idField, func(t *testing.T) {
			start := `{"type":"message_start","message":{` + idField + `"role":"assistant","model":"deepseek-flash","provider_detail":{"zero":0,"enabled":false,"large_integer":9007199254740993}},"vendor_meta":{"keep":true}}`
			events := []string{
				start,
				`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"reason one"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"content_block_start","index":2,"content_block":{"type":"thinking","thinking":"reason two"}}`,
				`{"type":"content_block_stop","index":2}`,
				`{"type":"content_block_start","index":3,"content_block":{"type":"thinking","thinking":"signed","signature":"real-signature"}}`,
				`{"type":"content_block_stop","index":3}`,
				`{"type":"message_stop"}`,
			}
			output := runThinkingSignatureStream(t, thinkingSignatureRelayInfo(), events)
			require.Len(t, output, len(events)+2)
			id := gjson.Get(output[0], "message.id").String()
			require.Regexp(t, `^msg_[0-9a-f]{32}$`, id)
			require.Equal(t, "0", gjson.Get(output[0], "message.provider_detail.zero").Raw)
			require.Equal(t, "false", gjson.Get(output[0], "message.provider_detail.enabled").Raw)
			require.Equal(t, "9007199254740993", gjson.Get(output[0], "message.provider_detail.large_integer").Raw)
			require.True(t, gjson.Get(output[0], "vendor_meta.keep").Bool())
			originalIndex := 1
			for i, data := range output[1:] {
				if originalIndex < len(events) && data == events[originalIndex] {
					originalIndex++
					continue
				}
				var delta dto.ClaudeResponse
				require.NoError(t, common.UnmarshalJsonStr(data, &delta))
				require.Equal(t, "content_block_delta", delta.Type)
				require.Equal(t, "signature_delta", delta.Delta.Type)
				require.Equal(t, id, delta.Delta.Signature)
				require.Contains(t, []int{0, 2}, delta.GetIndex())
				var stop dto.ClaudeResponse
				require.NoError(t, common.UnmarshalJsonStr(output[i+2], &stop))
				require.Equal(t, "content_block_stop", stop.Type)
				require.Equal(t, delta.GetIndex(), stop.GetIndex())
			}
			require.Equal(t, len(events), originalIndex, "all other upstream events must remain unchanged")
		})
	}
}

func TestHandleStreamResponseDataMissingIDDoesNotReusePreviousMessageID(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{"id":"msg-upstream","role":"assistant"}}`,
		`{"type":"message_stop"}`,
	}
	for range 2 {
		events = append(events,
			`{"type":"message_start","message":{"role":"assistant"}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"reason"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_stop"}`,
		)
	}
	output := runThinkingSignatureStream(t, thinkingSignatureRelayInfo(), events)
	var ids, signatures []string
	for _, data := range output {
		switch gjson.Get(data, "type").String() {
		case "message_start":
			ids = append(ids, gjson.Get(data, "message.id").String())
		case "content_block_delta":
			signatures = append(signatures, gjson.Get(data, "delta.signature").String())
		}
	}
	require.Len(t, ids, 3)
	require.Equal(t, "msg-upstream", ids[0])
	require.NotEqual(t, ids[0], ids[1])
	require.NotEqual(t, ids[1], ids[2])
	require.Equal(t, ids[1:], signatures)
}

func TestHandleStreamResponseDataThinkingSignatureAnthropicChannelOnly(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{"id":"msg-upstream","role":"assistant"}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"reasoning"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_stop"}`,
	}
	for _, channelType := range []int{constant.ChannelTypeAws, constant.ChannelTypeOpenAI} {
		info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: channelType}}
		require.Equal(t, events, runThinkingSignatureStream(t, info, events))
		withoutID := append([]string(nil), events...)
		withoutID[0] = `{"type":"message_start","message":{"role":"assistant"}}`
		require.Equal(t, withoutID, runThinkingSignatureStream(t, info, withoutID))
	}
}

func runThinkingSignatureStream(t *testing.T, info *relaycommon.RelayInfo, events []string) []string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	claudeInfo := &ClaudeResponseInfo{ResponseId: "local-generated-id", Usage: &dto.Usage{}}
	for _, data := range events {
		require.Nil(t, HandleStreamResponseData(c, info, claudeInfo, data))
	}
	var output []string
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if strings.HasPrefix(line, "data: ") {
			output = append(output, strings.TrimPrefix(line, "data: "))
		}
	}
	return output
}
