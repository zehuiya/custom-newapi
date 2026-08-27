package claude

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestConvertClaudeRequestFillsMissingThinkingBeforeToolUse(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		thinking *dto.Thinking
	}{
		{name: "flash without thinking config", model: "deepseek-v4-flash"},
		{name: "pro with thinking enabled", model: "deepseek-v4-pro", thinking: &dto.Thinking{Type: "enabled"}},
		{name: "provider-prefixed model with thinking disabled", model: "anthropic:deepseek-v4-flash", thinking: &dto.Thinking{Type: "disabled"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := &dto.ClaudeRequest{
				Model:    tt.model,
				Thinking: tt.thinking,
				Messages: []dto.ClaudeMessage{
					{Role: "user", Content: "find the weather"},
					{
						Role: "assistant",
						Content: []any{
							map[string]any{"type": "text", "text": "I'll check."},
							map[string]any{
								"type": "tool_use",
								"id":   "toolu_1",
								"name": "weather",
								"input": map[string]any{
									"city": "Hangzhou",
								},
							},
						},
					},
				},
			}

			converted, err := (&Adaptor{}).ConvertClaudeRequest(nil, &relaycommon.RelayInfo{
				OriginModelName: tt.model,
				RequestURLPath:  "/v1/messages",
				ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: tt.model,
				},
			}, request)
			require.NoError(t, err)
			require.Same(t, request, converted)

			content := request.Messages[1].Content.([]any)
			require.Len(t, content, 3)
			require.Equal(t, map[string]any{
				"type":     "thinking",
				"thinking": "...[truncated]",
			}, content[0])
			require.Equal(t, "text", content[1].(map[string]any)["type"])
			require.Equal(t, "tool_use", content[2].(map[string]any)["type"])
		})
	}
}

func TestConvertClaudeRequestFillsEveryMissingAssistantTurn(t *testing.T) {
	request := &dto.ClaudeRequest{
		Model: "deepseek-v4-pro",
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "first task"},
			{
				Role: "assistant",
				Content: []any{
					map[string]any{"type": "tool_use", "id": "toolu_1", "name": "first", "input": map[string]any{}},
				},
			},
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "one"},
				},
			},
			{
				Role: "assistant",
				Content: []any{
					map[string]any{"type": "tool_use", "id": "toolu_2", "name": "second", "input": map[string]any{}},
				},
			},
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "tool_result", "tool_use_id": "toolu_2", "content": "two"},
				},
			},
			{Role: "assistant", Content: "finished"},
			{Role: "user", Content: "one more question"},
		},
	}

	_, err := (&Adaptor{}).ConvertClaudeRequest(nil, &relaycommon.RelayInfo{
		RequestURLPath: "/v1/messages",
		ChannelMeta:    &relaycommon.ChannelMeta{UpstreamModelName: request.Model},
	}, request)
	require.NoError(t, err)

	for _, messageIndex := range []int{1, 3} {
		content := request.Messages[messageIndex].Content.([]any)
		require.Len(t, content, 2)
		require.Equal(t, "thinking", content[0].(map[string]any)["type"])
		require.Equal(t, "...[truncated]", content[0].(map[string]any)["thinking"])
		require.Equal(t, "tool_use", content[1].(map[string]any)["type"])
	}
	finalContent := request.Messages[5].Content.([]any)
	require.Len(t, finalContent, 2)
	require.Equal(t, map[string]any{
		"type":     "thinking",
		"thinking": "...[truncated]",
	}, finalContent[0])
	require.Equal(t, map[string]any{
		"type": "text",
		"text": "finished",
	}, finalContent[1])
	require.Equal(t, "one more question", request.Messages[6].Content)
}

func TestConvertClaudeRequestPreservesExistingThinkingAndUnknownFields(t *testing.T) {
	existingThinking := "real reasoning"
	request := &dto.ClaudeRequest{
		Model: "deepseek-v4-flash",
		Messages: []dto.ClaudeMessage{
			{
				Role: "assistant",
				Content: []any{
					map[string]any{
						"type":         "thinking",
						"thinking":     existingThinking,
						"signature":    "opaque-signature",
						"future_field": "must-survive",
					},
					map[string]any{"type": "tool_use", "id": "toolu_1", "name": "lookup", "input": map[string]any{}},
				},
			},
		},
	}

	originalContent := request.Messages[0].Content
	_, err := (&Adaptor{}).ConvertClaudeRequest(nil, &relaycommon.RelayInfo{
		RequestURLPath: "/v1/messages",
		ChannelMeta:    &relaycommon.ChannelMeta{UpstreamModelName: request.Model},
	}, request)
	require.NoError(t, err)
	require.Equal(t, originalContent, request.Messages[0].Content)
}

func TestConvertClaudeRequestPreservesExistingEmptyThinkingBlock(t *testing.T) {
	request := &dto.ClaudeRequest{
		Model: "deepseek-v4-pro",
		Messages: []dto.ClaudeMessage{
			{
				Role: "assistant",
				Content: []dto.ClaudeMediaMessage{
					{Type: "thinking", Thinking: stringPointer(""), Signature: "opaque-signature"},
					{Type: "tool_use", Id: "toolu_1", Name: "lookup", Input: map[string]any{}},
				},
			},
		},
	}

	originalContent := request.Messages[0].Content
	_, err := (&Adaptor{}).ConvertClaudeRequest(nil, &relaycommon.RelayInfo{
		RequestURLPath: "/v1/messages",
		ChannelMeta:    &relaycommon.ChannelMeta{UpstreamModelName: request.Model},
	}, request)
	require.NoError(t, err)
	require.Equal(t, originalContent, request.Messages[0].Content)
}

func TestConvertClaudeRequestLeavesOtherModelsUnchanged(t *testing.T) {
	request := &dto.ClaudeRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []dto.ClaudeMessage{
			{
				Role: "assistant",
				Content: []any{
					map[string]any{"type": "tool_use", "id": "toolu_1", "name": "lookup", "input": map[string]any{}},
				},
			},
		},
	}

	originalContent := request.Messages[0].Content
	_, err := (&Adaptor{}).ConvertClaudeRequest(nil, &relaycommon.RelayInfo{
		RequestURLPath: "/v1/messages",
		ChannelMeta:    &relaycommon.ChannelMeta{UpstreamModelName: request.Model},
	}, request)
	require.NoError(t, err)
	require.Equal(t, originalContent, request.Messages[0].Content)
}

func TestConvertClaudeRequestUsesFinalUpstreamModelForCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		requestModel  string
		originModel   string
		upstreamModel string
		wantPatched   bool
	}{
		{
			name:          "public alias mapped to affected upstream",
			requestModel:  "deepseek-public-alias",
			originModel:   "deepseek-public-alias",
			upstreamModel: "deepseek-v4-pro",
			wantPatched:   true,
		},
		{
			name:          "public alias mapped to versioned affected upstream",
			requestModel:  "deepseek-public-alias",
			originModel:   "deepseek-public-alias",
			upstreamModel: "deepseek-v4-pro-0813",
			wantPatched:   true,
		},
		{
			name:          "provider-prefixed versioned affected upstream",
			requestModel:  "deepseek-public-alias",
			originModel:   "deepseek-public-alias",
			upstreamModel: "anthropic:deepseek-v4-flash-20260813",
			wantPatched:   true,
		},
		{
			name:          "affected public name mapped to official Claude",
			requestModel:  "deepseek-v4-pro",
			originModel:   "anthropic:deepseek-v4-pro",
			upstreamModel: "claude-sonnet-4-20250514",
			wantPatched:   false,
		},
		{
			name:          "containing pro model name is matched",
			requestModel:  "deepseek-public-alias",
			originModel:   "deepseek-public-alias",
			upstreamModel: "vendor/deepseek-v4-pro-0813-preview",
			wantPatched:   true,
		},
		{
			name:          "containing flash model name is matched",
			requestModel:  "deepseek-public-alias",
			originModel:   "deepseek-public-alias",
			upstreamModel: "vendor-deepseek-v4-flash-preview",
			wantPatched:   true,
		},
		{
			name:          "nearby model name without target substring is not matched",
			requestModel:  "deepseek-public-alias",
			originModel:   "deepseek-public-alias",
			upstreamModel: "deepseek-v4-fast",
			wantPatched:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := &dto.ClaudeRequest{
				Model: tt.requestModel,
				Messages: []dto.ClaudeMessage{
					{Role: "assistant", Content: "answer"},
				},
			}
			_, err := (&Adaptor{}).ConvertClaudeRequest(nil, &relaycommon.RelayInfo{
				OriginModelName: tt.originModel,
				RequestURLPath:  "/v1/messages",
				ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: tt.upstreamModel,
				},
			}, request)
			require.NoError(t, err)

			_, patched := request.Messages[0].Content.([]any)
			require.Equal(t, tt.wantPatched, patched)
		})
	}
}

func TestConvertClaudeRequestHandlesMissingRelayInfoAndChannelMeta(t *testing.T) {
	tests := []struct {
		name        string
		info        *relaycommon.RelayInfo
		wantPatched bool
	}{
		{name: "nil relay info"},
		{
			name: "nil channel meta",
			info: &relaycommon.RelayInfo{
				OriginModelName: "deepseek-v4-pro",
				RequestURLPath:  "/v1/messages",
			},
			wantPatched: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := &dto.ClaudeRequest{
				Model: "deepseek-v4-flash",
				Messages: []dto.ClaudeMessage{
					{Role: "assistant", Content: "answer"},
				},
			}
			_, err := (&Adaptor{}).ConvertClaudeRequest(nil, tt.info, request)
			require.NoError(t, err)
			_, patched := request.Messages[0].Content.([]any)
			require.Equal(t, tt.wantPatched, patched)
		})
	}
}

func TestConvertClaudeRequestOnlyPatchesMessagesPath(t *testing.T) {
	tests := []struct {
		name        string
		requestPath string
		wantPatched bool
	}{
		{name: "messages path", requestPath: "/v1/messages", wantPatched: true},
		{name: "messages path with query", requestPath: "/v1/messages?beta=true", wantPatched: true},
		{name: "absolute messages URL", requestPath: "https://gateway.example/v1/messages?beta=true", wantPatched: true},
		{name: "chat completions path", requestPath: "/v1/chat/completions"},
		{name: "messages subpath", requestPath: "/v1/messages/batches"},
		{name: "missing path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := &dto.ClaudeRequest{
				Model: "public-alias",
				Messages: []dto.ClaudeMessage{
					{Role: "assistant", Content: "answer"},
				},
			}
			_, err := (&Adaptor{}).ConvertClaudeRequest(nil, &relaycommon.RelayInfo{
				RequestURLPath: tt.requestPath,
				ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: "vendor/deepseek-v4-pro-0813",
				},
			}, request)
			require.NoError(t, err)

			_, patched := request.Messages[0].Content.([]any)
			require.Equal(t, tt.wantPatched, patched)
		})
	}
}

func stringPointer(value string) *string {
	return &value
}
