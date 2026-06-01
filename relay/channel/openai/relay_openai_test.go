package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
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

func TestSyntheticCacheInjectionKeepsOpenAIPromptTokensInclusive(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     20785,
		CompletionTokens: 104,
		TotalTokens:      20889,
	}

	changed := injectSyntheticCacheInfoForOpenAIUsage(usage)

	require.True(t, changed)
	require.Equal(t, 20785, usage.PromptTokens)
	require.Equal(t, 20889, usage.TotalTokens)
	require.GreaterOrEqual(t, usage.PromptTokensDetails.CachedTokens, 20785*50/100)
	require.LessOrEqual(t, usage.PromptTokensDetails.CachedTokens, 20785*90/100)
	require.GreaterOrEqual(t, usage.PromptTokens-usage.PromptTokensDetails.CachedTokens, 0)
}

func TestSyntheticCacheInjectionDoesNotOverwriteUpstreamCacheTokens(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens: 5000,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 1234,
		},
	}

	changed := injectSyntheticCacheInfoForOpenAIUsage(usage)

	require.False(t, changed)
	require.Equal(t, 5000, usage.PromptTokens)
	require.Equal(t, 1234, usage.PromptTokensDetails.CachedTokens)
}
