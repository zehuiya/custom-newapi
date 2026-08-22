package openai

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenaiHandlerWithUsageDoesNotDoubleCountPromptAndInputTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"usage": {
				"prompt_tokens": 5413,
				"input_tokens": 5413,
				"completion_tokens": 16,
				"output_tokens": 16,
				"total_tokens": 5429,
				"prompt_tokens_details": {
					"cached_tokens": 5376
				}
			}
		}`)),
	}

	usage, err := OpenaiHandlerWithUsage(c, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
	}, resp)

	require.Nil(t, err)
	require.Equal(t, 5413, usage.PromptTokens)
	require.Equal(t, 16, usage.CompletionTokens)
	require.Equal(t, 5429, usage.TotalTokens)
	require.Equal(t, 5376, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, "openai", usage.UsageSemantic)
}

func TestSendStreamDataPreservesReasoningContentWithoutThinkTags(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var legacySettings dto.ChannelSettings
	require.NoError(t, common.Unmarshal([]byte(`{"thinking_to_content":true}`), &legacySettings))
	data := `{"choices":[{"index":0,"delta":{"reasoning_content":"hidden thought","content":"answer"}}]}`

	for _, forceFormat := range []bool{false, true} {
		t.Run(fmt.Sprintf("force_format_%t", forceFormat), func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: legacySettings},
			}

			require.NoError(t, sendStreamData(c, info, data, forceFormat))
			require.Contains(t, w.Body.String(), `"reasoning_content":"hidden thought"`)
			require.Contains(t, w.Body.String(), `"content":"answer"`)
			require.NotContains(t, w.Body.String(), "<think>")
			require.NotContains(t, w.Body.String(), "</think>")
		})
	}
}

func TestSyntheticCacheInjectionKeepsOpenAIPromptTokensInclusive(t *testing.T) {
	percentage := 60
	usage := &dto.Usage{
		PromptTokens:     20785,
		CompletionTokens: 104,
		TotalTokens:      20889,
	}

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelSetting: dto.ChannelSettings{
			CacheEnabled:       true,
			CachePercentageMin: &percentage,
			CachePercentageMax: &percentage,
		},
	}}
	changed := injectSyntheticCacheInfoForOpenAIUsage(usage, info)

	require.True(t, changed)
	require.Equal(t, 20785, usage.PromptTokens)
	require.Equal(t, 20889, usage.TotalTokens)
	require.Equal(t, 20785*percentage/100, usage.PromptTokensDetails.CachedTokens)
	require.GreaterOrEqual(t, usage.PromptTokens-usage.PromptTokensDetails.CachedTokens, 0)
}

func TestSyntheticCacheInjectionDoesNotOverwriteUpstreamCacheTokens(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens: 5000,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 1234,
		},
	}

	changed := injectSyntheticCacheInfoForOpenAIUsage(usage, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{CacheEnabled: true}},
	})

	require.False(t, changed)
	require.Equal(t, 5000, usage.PromptTokens)
	require.Equal(t, 1234, usage.PromptTokensDetails.CachedTokens)
}

func TestSyntheticCacheInjectionOverrideRaisesLowerOpenAICache(t *testing.T) {
	percentage := 85
	usage := &dto.Usage{
		PromptTokens:         10000,
		CompletionTokens:     100,
		TotalTokens:          10100,
		PromptCacheHitTokens: 500,
		PromptTokensDetails:  dto.InputTokenDetails{CachedTokens: 500},
		InputTokensDetails:   &dto.InputTokenDetails{CachedTokens: 500},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{
		CacheEnabled:         true,
		CacheOverrideEnabled: true,
		CachePercentageMin:   &percentage,
		CachePercentageMax:   &percentage,
	}}}

	changed := injectSyntheticCacheInfoForOpenAIUsage(usage, info)

	require.True(t, changed)
	require.Equal(t, 10000, usage.PromptTokens)
	require.Equal(t, 10100, usage.TotalTokens)
	require.Equal(t, 8500, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 8500, usage.InputTokensDetails.CachedTokens)
	require.Equal(t, 8500, usage.PromptCacheHitTokens)
}

func TestSyntheticCacheInjectionOverridePreservesHigherOpenAICache(t *testing.T) {
	percentage := 85
	usage := &dto.Usage{
		PromptTokens: 10000,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 9000,
		},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{
		CacheEnabled:         true,
		CacheOverrideEnabled: true,
		CachePercentageMin:   &percentage,
		CachePercentageMax:   &percentage,
	}}}

	changed := injectSyntheticCacheInfoForOpenAIUsage(usage, info)

	require.False(t, changed)
	require.Equal(t, 9000, usage.PromptTokensDetails.CachedTokens)
}

func TestOpenaiHandlerSyntheticCacheDoesNotReplaceProviderSpecificCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"id": "chatcmpl-cache-test",
			"choices": [],
			"usage": {
				"prompt_tokens": 5000,
				"completion_tokens": 10,
				"total_tokens": 5010,
				"prompt_cache_hit_tokens": 2000
			}
		}`)),
	}
	percentage := 60
	info := &relaycommon.RelayInfo{
		RelayFormat:           types.RelayFormatOpenAI,
		ShouldInjectCacheInfo: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeDeepSeek,
			ChannelSetting: dto.ChannelSettings{
				CacheEnabled:       true,
				CachePercentageMin: &percentage,
				CachePercentageMax: &percentage,
			},
		},
	}

	usage, err := OpenaiHandler(c, info, resp)

	require.Nil(t, err)
	require.Equal(t, 2000, usage.PromptTokensDetails.CachedTokens)
	var body dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, 2000, body.Usage.PromptTokensDetails.CachedTokens)
}

func TestOpenaiHandlerSyntheticCacheOverrideRewritesLowerUpstreamCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"id":"chatcmpl-cache-override",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10000,"completion_tokens":100,"total_tokens":10100,"prompt_tokens_details":{"cached_tokens":500}}
		}`)),
	}
	percentage := 85
	info := &relaycommon.RelayInfo{
		RelayFormat:           types.RelayFormatOpenAI,
		ShouldInjectCacheInfo: true,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{
			CacheEnabled:         true,
			CacheOverrideEnabled: true,
			CachePercentageMin:   &percentage,
			CachePercentageMax:   &percentage,
		}},
	}

	usage, err := OpenaiHandler(c, info, resp)

	require.Nil(t, err)
	require.Equal(t, 10000, usage.PromptTokens)
	require.Equal(t, 8500, usage.PromptTokensDetails.CachedTokens)
	var body dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, 10000, body.Usage.PromptTokens)
	require.Equal(t, 8500, body.Usage.PromptTokensDetails.CachedTokens)
}

func TestOpenaiHandlerNoCacheMovesCacheReadIntoInputInResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"id": "chatcmpl-test",
			"choices": [],
			"usage": {
				"prompt_tokens": 130,
				"input_tokens": 130,
				"completion_tokens": 20,
				"total_tokens": 150,
				"prompt_cache_hit_tokens": 30,
				"prompt_tokens_details": {
					"cached_tokens": 30
				},
				"input_tokens_details": {
					"cached_tokens": 30
				}
			}
		}`)),
	}

	usage, err := OpenaiHandler(c, &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelName:    "openai",
			ChannelSetting: dto.ChannelSettings{NoCacheEnabled: true},
		},
	}, resp)

	require.Nil(t, err)
	require.Equal(t, 160, usage.PromptTokens)
	require.Equal(t, 160, usage.InputTokens)
	require.Equal(t, 20, usage.CompletionTokens)
	require.Equal(t, 180, usage.TotalTokens)
	require.Equal(t, 0, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 0, usage.PromptCacheHitTokens)

	var body dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, 160, body.Usage.PromptTokens)
	require.Equal(t, 160, body.Usage.InputTokens)
	require.Equal(t, 20, body.Usage.CompletionTokens)
	require.Equal(t, 180, body.Usage.TotalTokens)
	require.Equal(t, 0, body.Usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 0, body.Usage.PromptCacheHitTokens)
	require.NotNil(t, body.Usage.InputTokensDetails)
	require.Equal(t, 0, body.Usage.InputTokensDetails.CachedTokens)
}

func TestOaiResponsesHandlerNoCacheMovesCacheReadIntoInputResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"id": "resp-test",
			"object": "response",
			"output": [],
			"usage": {
				"input_tokens": 130,
				"output_tokens": 20,
				"total_tokens": 150,
				"input_tokens_details": {
					"cached_tokens": 30
				}
			}
		}`)),
	}

	usage, err := OaiResponsesHandler(c, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelName:    "responses",
			ChannelSetting: dto.ChannelSettings{NoCacheEnabled: true},
		},
	}, resp)

	require.Nil(t, err)
	require.Equal(t, 160, usage.PromptTokens)
	require.Equal(t, 160, usage.InputTokens)
	require.Equal(t, 20, usage.CompletionTokens)
	require.Equal(t, 180, usage.TotalTokens)
	require.Equal(t, 0, usage.PromptTokensDetails.CachedTokens)

	var body dto.OpenAIResponsesResponse
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &body))
	require.Greater(t, body.CreatedAt, 0)
	require.NotNil(t, body.Usage)
	require.Equal(t, 160, body.Usage.InputTokens)
	require.Equal(t, 20, body.Usage.OutputTokens)
	require.Equal(t, 180, body.Usage.TotalTokens)
	require.NotNil(t, body.Usage.InputTokensDetails)
	require.Equal(t, 0, body.Usage.InputTokensDetails.CachedTokens)
}

func TestOaiResponsesHandlerFillsMissingCreatedAtAndPreservesResponsesUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const createdAt = int64(1_700_000_001)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"id": "resp-created-at-test",
			"object": "response",
			"output": [],
			"vendor_large_integer": 900719925474099312345,
			"usage": {
				"input_tokens": 88,
				"output_tokens": 31,
				"total_tokens": 119
			}
		}`)),
	}

	usage, err := OaiResponsesHandler(c, &relaycommon.RelayInfo{
		StartTime: time.Unix(createdAt, 0),
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelName: "responses",
		},
	}, resp)

	require.Nil(t, err)
	require.Equal(t, 88, usage.InputTokens)
	require.Equal(t, 88, usage.PromptTokens)
	require.Equal(t, 31, usage.OutputTokens)
	require.Equal(t, 31, usage.CompletionTokens)
	require.Equal(t, 119, usage.TotalTokens)
	body := w.Body.Bytes()
	require.Equal(t, createdAt, gjson.GetBytes(body, "created_at").Int())
	require.Equal(t, int64(88), gjson.GetBytes(body, "usage.input_tokens").Int())
	require.Equal(t, int64(31), gjson.GetBytes(body, "usage.output_tokens").Int())
	require.Equal(t, int64(119), gjson.GetBytes(body, "usage.total_tokens").Int())
	require.False(t, gjson.GetBytes(body, "usage.prompt_tokens").Exists())
	require.False(t, gjson.GetBytes(body, "usage.completion_tokens").Exists())
	require.Contains(t, string(body), `"vendor_large_integer": 900719925474099312345`)
}

func TestOaiResponsesHandlerPreservesExistingCreatedAt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const upstreamCreatedAt = int64(1_690_000_123)
	responseBody := `{
		"id": "resp-existing-created-at",
		"object": "response",
		"created_at": 1690000123,
		"output": [],
		"usage": {"input_tokens": 1, "output_tokens": 2, "total_tokens": 3}
	}`
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(responseBody)),
	}

	_, err := OaiResponsesHandler(c, &relaycommon.RelayInfo{
		StartTime:   time.Unix(1_700_000_001, 0),
		ChannelMeta: &relaycommon.ChannelMeta{},
	}, resp)

	require.Nil(t, err)
	require.Equal(t, upstreamCreatedAt, gjson.GetBytes(w.Body.Bytes(), "created_at").Int())
	require.Equal(t, responseBody, w.Body.String())
}

func TestOaiResponsesStreamHandlerFillsCreatedAtAndPreservesResponsesUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() {
		constant.StreamingTimeout = oldStreamingTimeout
	})

	const createdAt = int64(1_700_000_001)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	sse := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp-stream-test","object":"response","status":"in_progress","output":[],"usage":null}}`,
		`data: {"type":"response.output_text.delta","delta":"ok"}`,
		`data: {"type":"response.completed","response":{"id":"resp-stream-test","object":"response","status":"completed","output":[],"usage":{"input_tokens":88,"output_tokens":31,"total_tokens":119}}}`,
		`data: [DONE]`,
	}, "\n\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(sse)),
	}

	usage, err := OaiResponsesStreamHandler(c, &relaycommon.RelayInfo{
		StartTime: time.Unix(createdAt, 0),
		RelayMode: relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelName: "responses",
		},
	}, resp)

	require.Nil(t, err)
	require.Equal(t, 88, usage.InputTokens)
	require.Equal(t, 88, usage.PromptTokens)
	require.Equal(t, 31, usage.OutputTokens)
	require.Equal(t, 31, usage.CompletionTokens)
	require.Equal(t, 119, usage.TotalTokens)

	streamEvents := responsesStreamEventsByType(w.Body.String())
	createdEvent, ok := streamEvents["response.created"]
	require.True(t, ok)
	completedEvent, ok := streamEvents["response.completed"]
	require.True(t, ok)
	deltaEvent, ok := streamEvents["response.output_text.delta"]
	require.True(t, ok)
	require.Equal(t, createdAt, gjson.Get(createdEvent, "response.created_at").Int())
	require.Equal(t, createdAt, gjson.Get(completedEvent, "response.created_at").Int())
	require.False(t, gjson.Get(deltaEvent, "response").Exists())
	require.False(t, gjson.Get(deltaEvent, "created_at").Exists())
	require.Equal(t, int64(88), gjson.Get(completedEvent, "response.usage.input_tokens").Int())
	require.Equal(t, int64(31), gjson.Get(completedEvent, "response.usage.output_tokens").Int())
	require.Equal(t, int64(119), gjson.Get(completedEvent, "response.usage.total_tokens").Int())
	require.False(t, gjson.Get(completedEvent, "response.usage.prompt_tokens").Exists())
	require.False(t, gjson.Get(completedEvent, "response.usage.completion_tokens").Exists())
}

func TestEnsureResponsesCreatedAtPreservesExistingNestedValueAndSkipsDelta(t *testing.T) {
	const fallbackCreatedAt = int64(1_700_000_001)
	existing := []byte(`{"type":"response.created","response":{"id":"resp-test","created_at":1690000123}}`)
	patched, effectiveCreatedAt, err := ensureResponsesCreatedAt(existing, true, fallbackCreatedAt)
	require.NoError(t, err)
	require.Equal(t, existing, patched)
	require.Equal(t, int64(1_690_000_123), effectiveCreatedAt)

	delta := []byte(`{"type":"response.output_text.delta","delta":"ok"}`)
	patched, effectiveCreatedAt, err = ensureResponsesCreatedAt(delta, true, fallbackCreatedAt)
	require.NoError(t, err)
	require.Equal(t, delta, patched)
	require.Zero(t, effectiveCreatedAt)
}

func responsesStreamEventsByType(body string) map[string]string {
	events := make(map[string]string)
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		eventType := gjson.Get(data, "type").String()
		if eventType != "" {
			events[eventType] = data
		}
	}
	return events
}

func TestRewriteOpenAIStreamUsageDataWithNoCacheUsage(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelName:    "openai",
			ChannelSetting: dto.ChannelSettings{NoCacheEnabled: true},
		},
	}
	usage := &dto.Usage{
		PromptTokens:     130,
		CompletionTokens: 20,
		TotalTokens:      150,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 30,
		},
	}
	require.True(t, service.NormalizeNoCacheUsageForRelay(nil, info, usage))

	data := `{"id":"chunk-test","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":130,"completion_tokens":20,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":30}}}`
	modified := rewriteOpenAIStreamUsageData(data, usage)

	var streamResp dto.ChatCompletionsStreamResponse
	require.NoError(t, common.UnmarshalJsonStr(modified, &streamResp))
	require.NotNil(t, streamResp.Usage)
	require.Equal(t, 160, streamResp.Usage.PromptTokens)
	require.Equal(t, 180, streamResp.Usage.TotalTokens)
	require.Equal(t, 0, streamResp.Usage.PromptTokensDetails.CachedTokens)
}

func TestOpenaiHandlerFillsMissingReasoningTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	reasoningText := "hidden reasoning content"
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"id": "chatcmpl-test",
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {"role": "assistant", "content": "ok", "reasoning_content": "` + reasoningText + `"},
				"finish_reason": "stop"
			}],
			"usage": {
				"prompt_tokens": 10,
				"completion_tokens": 5,
				"total_tokens": 15,
				"completion_tokens_details": {"reasoning_tokens": 0}
			}
		}`)),
	}

	usage, err := OpenaiHandler(c, &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "test-model",
		},
	}, resp)

	outputEstimate := service.CountTextToken("ok", "test-model")
	reasoningEstimate := service.CountTextToken(reasoningText, "test-model")
	expected := int(math.Round(float64(5) * float64(reasoningEstimate) / float64(outputEstimate+reasoningEstimate)))
	require.Nil(t, err)
	require.Greater(t, expected, 0)
	require.Equal(t, expected, usage.CompletionTokenDetails.ReasoningTokens)

	var body dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, expected, body.Usage.CompletionTokenDetails.ReasoningTokens)
}

func TestOaiStreamHandlerFillsMissingReasoningTokensInUsageChunk(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() {
		constant.StreamingTimeout = oldStreamingTimeout
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	sse := strings.Join([]string{
		`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"hidden stream "},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"reasoning_content":"reasoning","content":"ok"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"completion_tokens_details":{"reasoning_tokens":0}}}`,
		`data: [DONE]`,
	}, "\n\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(sse)),
	}

	usage, err := OaiStreamHandler(c, &relaycommon.RelayInfo{
		RelayFormat:        types.RelayFormatOpenAI,
		RelayMode:          relayconstant.RelayModeChatCompletions,
		ShouldIncludeUsage: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "test-model",
		},
	}, resp)

	outputEstimate := service.CountTextToken("ok", "test-model")
	reasoningEstimate := service.CountTextToken("hidden stream reasoning", "test-model")
	expected := int(math.Round(float64(5) * float64(reasoningEstimate) / float64(outputEstimate+reasoningEstimate)))
	require.Nil(t, err)
	require.Greater(t, expected, 0)
	require.Equal(t, expected, usage.CompletionTokenDetails.ReasoningTokens)
	require.Contains(t, w.Body.String(), fmt.Sprintf(`"reasoning_tokens":%d`, expected))
}

func TestRewriteResponsesUsagePayloadWithNoCacheStreamUsage(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelName:    "responses",
			ChannelSetting: dto.ChannelSettings{NoCacheEnabled: true},
		},
	}
	usage := &dto.Usage{}
	fillRelayUsageFromResponsesUsage(usage, &dto.Usage{
		InputTokens:  130,
		OutputTokens: 20,
		TotalTokens:  150,
		InputTokensDetails: &dto.InputTokenDetails{
			CachedTokens: 30,
		},
	})
	require.True(t, service.NormalizeNoCacheUsageForRelay(nil, info, usage))

	data := []byte(`{"type":"response.completed","response":{"id":"resp-test","usage":{"input_tokens":130,"output_tokens":20,"total_tokens":150,"input_tokens_details":{"cached_tokens":30}}}}`)
	modified, changed := rewriteResponsesUsagePayload(data, true, usage)

	require.True(t, changed)
	var streamResp dto.ResponsesStreamResponse
	require.NoError(t, common.Unmarshal(modified, &streamResp))
	require.NotNil(t, streamResp.Response)
	require.NotNil(t, streamResp.Response.Usage)
	require.Equal(t, 160, streamResp.Response.Usage.InputTokens)
	require.Equal(t, 20, streamResp.Response.Usage.OutputTokens)
	require.Equal(t, 180, streamResp.Response.Usage.TotalTokens)
	require.NotNil(t, streamResp.Response.Usage.InputTokensDetails)
	require.Equal(t, 0, streamResp.Response.Usage.InputTokensDetails.CachedTokens)
}
