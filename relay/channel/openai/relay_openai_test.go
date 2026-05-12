package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
