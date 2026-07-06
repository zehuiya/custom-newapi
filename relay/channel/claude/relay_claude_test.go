package claude

import (
	"encoding/base64"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFormatClaudeResponseInfo_MessageStart(t *testing.T) {
	claudeInfo := &ClaudeResponseInfo{
		Usage: &dto.Usage{},
	}
	claudeResponse := &dto.ClaudeResponse{
		Type: "message_start",
		Message: &dto.ClaudeMediaMessage{
			Id:    "msg_123",
			Model: "claude-3-5-sonnet",
			Usage: &dto.ClaudeUsage{
				InputTokens:              100,
				OutputTokens:             1,
				CacheCreationInputTokens: 50,
				CacheReadInputTokens:     30,
			},
		},
	}

	ok := FormatClaudeResponseInfo(claudeResponse, nil, claudeInfo)
	if !ok {
		t.Fatal("expected true")
	}
	if claudeInfo.Usage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", claudeInfo.Usage.PromptTokens)
	}
	if claudeInfo.Usage.PromptTokensDetails.CachedTokens != 30 {
		t.Errorf("CachedTokens = %d, want 30", claudeInfo.Usage.PromptTokensDetails.CachedTokens)
	}
	if claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens != 50 {
		t.Errorf("CachedCreationTokens = %d, want 50", claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens)
	}
	if claudeInfo.ResponseId != "msg_123" {
		t.Errorf("ResponseId = %s, want msg_123", claudeInfo.ResponseId)
	}
	if claudeInfo.Model != "claude-3-5-sonnet" {
		t.Errorf("Model = %s, want claude-3-5-sonnet", claudeInfo.Model)
	}
}

func TestFormatClaudeResponseInfo_MessageDelta_FullUsage(t *testing.T) {
	// message_start 先积累 usage
	claudeInfo := &ClaudeResponseInfo{
		Usage: &dto.Usage{
			PromptTokens: 100,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens:         30,
				CachedCreationTokens: 50,
			},
			CompletionTokens: 1,
		},
	}

	// message_delta 带完整 usage（原生 Anthropic 场景）
	claudeResponse := &dto.ClaudeResponse{
		Type: "message_delta",
		Usage: &dto.ClaudeUsage{
			InputTokens:              100,
			OutputTokens:             200,
			CacheCreationInputTokens: 50,
			CacheReadInputTokens:     30,
		},
	}

	ok := FormatClaudeResponseInfo(claudeResponse, nil, claudeInfo)
	if !ok {
		t.Fatal("expected true")
	}
	if claudeInfo.Usage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", claudeInfo.Usage.PromptTokens)
	}
	if claudeInfo.Usage.CompletionTokens != 200 {
		t.Errorf("CompletionTokens = %d, want 200", claudeInfo.Usage.CompletionTokens)
	}
	if claudeInfo.Usage.TotalTokens != 300 {
		t.Errorf("TotalTokens = %d, want 300", claudeInfo.Usage.TotalTokens)
	}
	if !claudeInfo.Done {
		t.Error("expected Done = true")
	}
}

func TestFormatClaudeResponseInfo_MessageDelta_OnlyOutputTokens(t *testing.T) {
	// 模拟 Bedrock: message_start 已积累 usage
	claudeInfo := &ClaudeResponseInfo{
		Usage: &dto.Usage{
			PromptTokens: 100,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens:         30,
				CachedCreationTokens: 50,
			},
			CompletionTokens:            1,
			ClaudeCacheCreation5mTokens: 10,
			ClaudeCacheCreation1hTokens: 20,
		},
	}

	// Bedrock 的 message_delta 只有 output_tokens，缺少 input_tokens 和 cache 字段
	claudeResponse := &dto.ClaudeResponse{
		Type: "message_delta",
		Usage: &dto.ClaudeUsage{
			OutputTokens: 200,
			// InputTokens, CacheCreationInputTokens, CacheReadInputTokens 都是 0
		},
	}

	ok := FormatClaudeResponseInfo(claudeResponse, nil, claudeInfo)
	if !ok {
		t.Fatal("expected true")
	}
	// PromptTokens 应保持 message_start 的值（因为 message_delta 的 InputTokens=0，不更新）
	if claudeInfo.Usage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", claudeInfo.Usage.PromptTokens)
	}
	if claudeInfo.Usage.CompletionTokens != 200 {
		t.Errorf("CompletionTokens = %d, want 200", claudeInfo.Usage.CompletionTokens)
	}
	if claudeInfo.Usage.TotalTokens != 300 {
		t.Errorf("TotalTokens = %d, want 300", claudeInfo.Usage.TotalTokens)
	}
	// cache 字段应保持 message_start 的值
	if claudeInfo.Usage.PromptTokensDetails.CachedTokens != 30 {
		t.Errorf("CachedTokens = %d, want 30", claudeInfo.Usage.PromptTokensDetails.CachedTokens)
	}
	if claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens != 50 {
		t.Errorf("CachedCreationTokens = %d, want 50", claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens)
	}
	if claudeInfo.Usage.ClaudeCacheCreation5mTokens != 10 {
		t.Errorf("ClaudeCacheCreation5mTokens = %d, want 10", claudeInfo.Usage.ClaudeCacheCreation5mTokens)
	}
	if claudeInfo.Usage.ClaudeCacheCreation1hTokens != 20 {
		t.Errorf("ClaudeCacheCreation1hTokens = %d, want 20", claudeInfo.Usage.ClaudeCacheCreation1hTokens)
	}
	if !claudeInfo.Done {
		t.Error("expected Done = true")
	}
}

func TestFormatClaudeResponseInfo_NilClaudeInfo(t *testing.T) {
	claudeResponse := &dto.ClaudeResponse{Type: "message_start"}
	ok := FormatClaudeResponseInfo(claudeResponse, nil, nil)
	if ok {
		t.Error("expected false for nil claudeInfo")
	}
}

func TestFormatClaudeResponseInfo_ContentBlockDelta(t *testing.T) {
	text := "hello"
	claudeInfo := &ClaudeResponseInfo{
		Usage:        &dto.Usage{},
		ResponseText: strings.Builder{},
	}
	claudeResponse := &dto.ClaudeResponse{
		Type: "content_block_delta",
		Delta: &dto.ClaudeMediaMessage{
			Text: &text,
		},
	}

	ok := FormatClaudeResponseInfo(claudeResponse, nil, claudeInfo)
	if !ok {
		t.Fatal("expected true")
	}
	if claudeInfo.ResponseText.String() != "hello" {
		t.Errorf("ResponseText = %q, want %q", claudeInfo.ResponseText.String(), "hello")
	}
	if claudeInfo.OutputText.String() != "hello" {
		t.Errorf("OutputText = %q, want %q", claudeInfo.OutputText.String(), "hello")
	}
}

func TestFormatClaudeResponseInfo_ContentBlockDeltaThinkingTracksReasoning(t *testing.T) {
	thinking := "hidden claude stream reasoning"
	claudeInfo := &ClaudeResponseInfo{
		Usage:        &dto.Usage{},
		ResponseText: strings.Builder{},
	}
	claudeResponse := &dto.ClaudeResponse{
		Type: "content_block_delta",
		Delta: &dto.ClaudeMediaMessage{
			Thinking: &thinking,
		},
	}

	ok := FormatClaudeResponseInfo(claudeResponse, nil, claudeInfo)

	require.True(t, ok)
	require.Equal(t, thinking, claudeInfo.ResponseText.String())
	require.Equal(t, thinking, claudeInfo.ReasoningText.String())
	require.Empty(t, claudeInfo.OutputText.String())
}

func TestBuildOpenAIStyleUsageFromClaudeUsage(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 20,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         30,
			CachedCreationTokens: 50,
		},
		ClaudeCacheCreation5mTokens: 10,
		ClaudeCacheCreation1hTokens: 20,
		UsageSemantic:               "anthropic",
	}

	openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(usage)

	if openAIUsage.PromptTokens != 180 {
		t.Fatalf("PromptTokens = %d, want 180", openAIUsage.PromptTokens)
	}
	if openAIUsage.InputTokens != 180 {
		t.Fatalf("InputTokens = %d, want 180", openAIUsage.InputTokens)
	}
	if openAIUsage.TotalTokens != 200 {
		t.Fatalf("TotalTokens = %d, want 200", openAIUsage.TotalTokens)
	}
	if openAIUsage.UsageSemantic != "openai" {
		t.Fatalf("UsageSemantic = %s, want openai", openAIUsage.UsageSemantic)
	}
	if openAIUsage.UsageSource != "anthropic" {
		t.Fatalf("UsageSource = %s, want anthropic", openAIUsage.UsageSource)
	}
}

func TestBuildOpenAIStyleUsageFromClaudeUsagePreservesCacheCreationRemainder(t *testing.T) {
	tests := []struct {
		name                    string
		cachedCreationTokens    int
		cacheCreationTokens5m   int
		cacheCreationTokens1h   int
		expectedTotalInputToken int
	}{
		{
			name:                    "prefers aggregate when it includes remainder",
			cachedCreationTokens:    50,
			cacheCreationTokens5m:   10,
			cacheCreationTokens1h:   20,
			expectedTotalInputToken: 180,
		},
		{
			name:                    "falls back to split tokens when aggregate missing",
			cachedCreationTokens:    0,
			cacheCreationTokens5m:   10,
			cacheCreationTokens1h:   20,
			expectedTotalInputToken: 160,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := &dto.Usage{
				PromptTokens:     100,
				CompletionTokens: 20,
				PromptTokensDetails: dto.InputTokenDetails{
					CachedTokens:         30,
					CachedCreationTokens: tt.cachedCreationTokens,
				},
				ClaudeCacheCreation5mTokens: tt.cacheCreationTokens5m,
				ClaudeCacheCreation1hTokens: tt.cacheCreationTokens1h,
				UsageSemantic:               "anthropic",
			}

			openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(usage)

			if openAIUsage.PromptTokens != tt.expectedTotalInputToken {
				t.Fatalf("PromptTokens = %d, want %d", openAIUsage.PromptTokens, tt.expectedTotalInputToken)
			}
			if openAIUsage.InputTokens != tt.expectedTotalInputToken {
				t.Fatalf("InputTokens = %d, want %d", openAIUsage.InputTokens, tt.expectedTotalInputToken)
			}
		})
	}
}

func TestBuildOpenAIStyleUsageFromClaudeUsageDefaultsAggregateCacheCreationTo5m(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 20,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         30,
			CachedCreationTokens: 50,
		},
		UsageSemantic: "anthropic",
	}

	openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(usage)

	require.Equal(t, 50, openAIUsage.ClaudeCacheCreation5mTokens)
	require.Equal(t, 0, openAIUsage.ClaudeCacheCreation1hTokens)
}

func TestNormalizeAnthropicInclusiveCacheUsageForSub2APIChannel(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     10088,
		CompletionTokens: 1,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 9984,
		},
		UsageSemantic: "anthropic",
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelName: "sub2api-deepseek",
		},
	}

	require.True(t, normalizeAnthropicInclusiveCacheUsage(info, usage))
	require.Equal(t, 104, usage.PromptTokens)
	require.Equal(t, 105, usage.TotalTokens)
	require.Equal(t, normalizedAnthropicInclusiveCacheUsageSource, usage.UsageSource)

	openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(usage)
	require.Equal(t, 10088, openAIUsage.PromptTokens)
	require.Equal(t, 10088, openAIUsage.InputTokens)
	require.Equal(t, 10089, openAIUsage.TotalTokens)
	require.Equal(t, 9984, openAIUsage.PromptTokensDetails.CachedTokens)
}

func TestNormalizeAnthropicInclusiveCacheUsageForSub2APIOpenAIStyleClaudeUsage(t *testing.T) {
	claudeInfo := &ClaudeResponseInfo{
		Usage: &dto.Usage{},
	}
	claudeResponse := &dto.ClaudeResponse{
		Type: "message_delta",
		Usage: &dto.ClaudeUsage{
			PromptTokens:     10088,
			CompletionTokens: 1,
			TotalTokens:      10089,
			PromptTokensDetails: &dto.InputTokenDetails{
				CachedTokens: 9984,
			},
		},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelName: "sub2api-deepseek",
		},
	}

	require.True(t, FormatClaudeResponseInfo(claudeResponse, nil, claudeInfo))
	require.Equal(t, 10088, claudeInfo.Usage.PromptTokens)
	require.Equal(t, 9984, claudeInfo.Usage.PromptTokensDetails.CachedTokens)
	require.True(t, normalizeAnthropicInclusiveCacheUsage(info, claudeInfo.Usage))
	require.Equal(t, 104, claudeInfo.Usage.PromptTokens)
	require.Equal(t, 105, claudeInfo.Usage.TotalTokens)
}

func TestNormalizeAnthropicInclusiveCacheUsageForExplicitChannelSetting(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     10088,
		CompletionTokens: 1,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 9984,
		},
		UsageSemantic: "anthropic",
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{
				ClaudeInputTokensIncludesCache: true,
			},
		},
	}

	require.True(t, normalizeAnthropicInclusiveCacheUsage(info, usage))
	require.Equal(t, 104, usage.PromptTokens)
	require.Equal(t, 105, usage.TotalTokens)
}

func TestNormalizeAnthropicInclusiveCacheUsageKeepsNormalAnthropicChannel(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     104,
		CompletionTokens: 1,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 9984,
		},
		UsageSemantic: "anthropic",
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelName: "anthropic",
		},
	}

	require.False(t, normalizeAnthropicInclusiveCacheUsage(info, usage))
	require.Equal(t, 104, usage.PromptTokens)

	openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(usage)
	require.Equal(t, 10088, openAIUsage.PromptTokens)
	require.Equal(t, 10088, openAIUsage.InputTokens)
	require.Equal(t, 10089, openAIUsage.TotalTokens)
	require.Equal(t, 9984, openAIUsage.PromptTokensDetails.CachedTokens)
}

func TestHandleClaudeResponseDataNoCacheNormalizesNativeUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelName: "claude-[no_cache]",
		},
	}
	claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	data := []byte(`{
		"id": "msg-test",
		"type": "message",
		"role": "assistant",
		"model": "claude-test",
		"content": [{"type": "text", "text": "ok"}],
		"stop_reason": "end_turn",
		"usage": {
			"input_tokens": 100,
			"cache_read_input_tokens": 30,
			"output_tokens": 20
		}
	}`)

	err := HandleClaudeResponseData(c, info, claudeInfo, &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
	}, data)

	require.Nil(t, err)
	require.Equal(t, 130, claudeInfo.Usage.PromptTokens)
	require.Equal(t, 20, claudeInfo.Usage.CompletionTokens)
	require.Equal(t, 150, claudeInfo.Usage.TotalTokens)
	require.Equal(t, 0, claudeInfo.Usage.PromptTokensDetails.CachedTokens)

	var body dto.ClaudeResponse
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &body))
	require.NotNil(t, body.Usage)
	require.Equal(t, 130, body.Usage.InputTokens)
	require.Equal(t, 0, body.Usage.CacheReadInputTokens)
	require.Equal(t, 20, body.Usage.OutputTokens)
}

func TestHandleClaudeResponseDataFillsReasoningTokensForOpenAIRelay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	reasoningText := "hidden claude reasoning"
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-test",
		},
	}
	claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	data := []byte(`{
		"id": "msg-test",
		"type": "message",
		"role": "assistant",
		"model": "claude-test",
		"content": [
			{"type": "thinking", "thinking": "` + reasoningText + `"},
			{"type": "text", "text": "ok"}
		],
		"stop_reason": "end_turn",
		"usage": {
			"input_tokens": 10,
			"output_tokens": 5
		}
	}`)

	err := HandleClaudeResponseData(c, info, claudeInfo, &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
	}, data)

	outputEstimate := service.CountTextToken("ok", "claude-test")
	reasoningEstimate := service.CountTextToken(reasoningText, "claude-test")
	expected := int(math.Round(float64(5) * float64(reasoningEstimate) / float64(outputEstimate+reasoningEstimate)))
	require.Nil(t, err)
	require.Greater(t, expected, 0)
	require.Equal(t, expected, claudeInfo.Usage.CompletionTokenDetails.ReasoningTokens)

	var body dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, expected, body.Usage.CompletionTokenDetails.ReasoningTokens)
}

func TestHandleStreamResponseDataNoCacheNormalizesMessageDeltaUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelName: "claude-[no_cache]",
		},
	}
	claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	data := `{"type":"message_delta","usage":{"input_tokens":100,"cache_read_input_tokens":30,"output_tokens":20},"delta":{"stop_reason":"end_turn"}}`

	err := HandleStreamResponseData(c, info, claudeInfo, data)

	require.Nil(t, err)
	require.Equal(t, 130, claudeInfo.Usage.PromptTokens)
	require.Equal(t, 20, claudeInfo.Usage.CompletionTokens)
	require.Equal(t, 150, claudeInfo.Usage.TotalTokens)
	require.Equal(t, 0, claudeInfo.Usage.PromptTokensDetails.CachedTokens)
	require.Contains(t, w.Body.String(), `"input_tokens":130`)
	require.Contains(t, w.Body.String(), `"cache_read_input_tokens":0`)
}

func TestHandleStreamResponseDataNoCacheNormalizesMessageStartUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelName: "claude-[no_cache]",
		},
	}
	claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	data := `{
		"type":"message_start",
		"message":{
			"id":"msg-test",
			"type":"message",
			"role":"assistant",
			"model":"claude-test",
			"content":[],
			"usage":{"input_tokens":100,"cache_read_input_tokens":30,"output_tokens":0}
		}
	}`

	err := HandleStreamResponseData(c, info, claudeInfo, data)

	require.Nil(t, err)
	require.Equal(t, 130, claudeInfo.Usage.PromptTokens)
	require.Equal(t, 0, claudeInfo.Usage.CompletionTokens)
	require.Equal(t, 130, claudeInfo.Usage.TotalTokens)
	require.Equal(t, 0, claudeInfo.Usage.PromptTokensDetails.CachedTokens)
	require.Contains(t, w.Body.String(), `"input_tokens":130`)
	require.Contains(t, w.Body.String(), `"cache_read_input_tokens":0`)
}

func TestHandleStreamFinalResponseNoCacheNormalizesOpenAIUsageChunk(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelName:       "claude-[no_cache]",
			UpstreamModelName: "claude-test",
		},
	}
	claudeInfo := &ClaudeResponseInfo{
		ResponseId: "msg-test",
		Model:      "claude-test",
		Done:       true,
		Usage: &dto.Usage{
			PromptTokens:     100,
			CompletionTokens: 20,
			TotalTokens:      120,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens: 30,
			},
		},
	}

	HandleStreamFinalResponse(c, info, claudeInfo)

	require.Equal(t, 130, claudeInfo.Usage.PromptTokens)
	require.Equal(t, 20, claudeInfo.Usage.CompletionTokens)
	require.Equal(t, 150, claudeInfo.Usage.TotalTokens)
	require.Equal(t, 0, claudeInfo.Usage.PromptTokensDetails.CachedTokens)
	require.Contains(t, w.Body.String(), `"prompt_tokens":130`)
	require.Contains(t, w.Body.String(), `"total_tokens":150`)
	require.Contains(t, w.Body.String(), `"cached_tokens":0`)
}

func TestHandleStreamFinalResponseFillsReasoningTokensForOpenAIRelay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	reasoningText := "hidden claude stream reasoning"
	info := &relaycommon.RelayInfo{
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-test",
		},
	}
	claudeInfo := &ClaudeResponseInfo{
		ResponseId: "msg-test",
		Model:      "claude-test",
		Done:       true,
		Usage: &dto.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}
	claudeInfo.ReasoningText.WriteString(reasoningText)
	claudeInfo.OutputText.WriteString("ok")

	HandleStreamFinalResponse(c, info, claudeInfo)

	outputEstimate := service.CountTextToken("ok", "claude-test")
	reasoningEstimate := service.CountTextToken(reasoningText, "claude-test")
	expected := int(math.Round(float64(5) * float64(reasoningEstimate) / float64(outputEstimate+reasoningEstimate)))
	require.Greater(t, expected, 0)
	require.Equal(t, expected, claudeInfo.Usage.CompletionTokenDetails.ReasoningTokens)
	require.Contains(t, w.Body.String(), `"reasoning_tokens":`+strconv.Itoa(expected))
}

func TestRequestOpenAI2ClaudeMessage_IgnoresUnsupportedFileContent(t *testing.T) {
	request := dto.GeneralOpenAIRequest{
		Model: "claude-3-5-sonnet",
		Messages: []dto.Message{
			{
				Role: "user",
				Content: []any{
					dto.MediaContent{
						Type: dto.ContentTypeText,
						Text: "see attachment",
					},
					dto.MediaContent{
						Type: dto.ContentTypeFile,
						File: &dto.MessageFile{
							FileName: "blob.bin",
							FileData: "JVBERi0xLjQK",
						},
					},
				},
			},
		},
	}

	claudeRequest, err := RequestOpenAI2ClaudeMessage(nil, request)
	require.NoError(t, err)
	require.Len(t, claudeRequest.Messages, 1)

	content, ok := claudeRequest.Messages[0].Content.([]dto.ClaudeMediaMessage)
	require.True(t, ok)
	require.Len(t, content, 1)
	require.Equal(t, "text", content[0].Type)
	require.NotNil(t, content[0].Text)
	require.Equal(t, "see attachment", *content[0].Text)
}

func TestRequestOpenAI2ClaudeMessage_SupportsPDFFileContent(t *testing.T) {
	request := dto.GeneralOpenAIRequest{
		Model: "claude-3-5-sonnet",
		Messages: []dto.Message{
			{
				Role: "user",
				Content: []any{
					dto.MediaContent{
						Type: dto.ContentTypeFile,
						File: &dto.MessageFile{
							FileName: "spec.pdf",
							FileData: "JVBERi0xLjQK",
						},
					},
					dto.MediaContent{
						Type: dto.ContentTypeText,
						Text: "summarize it",
					},
				},
			},
		},
	}

	claudeRequest, err := RequestOpenAI2ClaudeMessage(nil, request)
	require.NoError(t, err)
	require.Len(t, claudeRequest.Messages, 1)

	content, ok := claudeRequest.Messages[0].Content.([]dto.ClaudeMediaMessage)
	require.True(t, ok)
	require.Len(t, content, 2)
	require.Equal(t, "document", content[0].Type)
	require.NotNil(t, content[0].Source)
	require.Equal(t, "base64", content[0].Source.Type)
	require.Equal(t, "application/pdf", content[0].Source.MediaType)
	require.Equal(t, "JVBERi0xLjQK", content[0].Source.Data)
	require.Equal(t, "text", content[1].Type)
	require.NotNil(t, content[1].Text)
	require.Equal(t, "summarize it", *content[1].Text)
}

func TestRequestOpenAI2ClaudeMessage_ConvertsTextFileContentToText(t *testing.T) {
	request := dto.GeneralOpenAIRequest{
		Model: "claude-3-5-sonnet",
		Messages: []dto.Message{
			{
				Role: "user",
				Content: []any{
					dto.MediaContent{
						Type: dto.ContentTypeFile,
						File: &dto.MessageFile{
							FileName: "notes.txt",
							FileData: base64.StdEncoding.EncodeToString([]byte("alpha\nbeta")),
						},
					},
				},
			},
		},
	}

	claudeRequest, err := RequestOpenAI2ClaudeMessage(nil, request)
	require.NoError(t, err)
	require.Len(t, claudeRequest.Messages, 1)

	content, ok := claudeRequest.Messages[0].Content.([]dto.ClaudeMediaMessage)
	require.True(t, ok)
	require.Len(t, content, 1)
	require.Equal(t, "text", content[0].Type)
	require.NotNil(t, content[0].Text)
	require.Equal(t, "alpha\nbeta", *content[0].Text)
}
