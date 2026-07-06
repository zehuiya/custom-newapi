package service

import (
	"math"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestFillMissingReasoningTokensUsesContentRatio(t *testing.T) {
	usage := &dto.Usage{
		CompletionTokens: 200,
	}
	outputText := "visible answer"
	reasoningText := strings.Repeat("hidden reasoning ", 100)

	changed := FillMissingReasoningTokens(nil, usage, reasoningText, outputText, "test-model")

	outputEstimate := CountTextToken(outputText, "test-model")
	reasoningEstimate := CountTextToken(reasoningText, "test-model")
	expected := int(math.Round(float64(usage.CompletionTokens) * float64(reasoningEstimate) / float64(outputEstimate+reasoningEstimate)))
	require.True(t, changed)
	require.Greater(t, expected, 0)
	require.Equal(t, expected, usage.CompletionTokenDetails.ReasoningTokens)
	require.LessOrEqual(t, usage.CompletionTokenDetails.ReasoningTokens, usage.CompletionTokens)
}

func TestFillMissingReasoningTokensDoesNotExceedCompletionTokens(t *testing.T) {
	usage := &dto.Usage{
		CompletionTokens: 10,
	}
	reasoningText := strings.Repeat("hidden reasoning ", 100)

	changed := FillMissingReasoningTokens(nil, usage, reasoningText, "", "test-model")

	require.True(t, changed)
	require.Equal(t, 10, usage.CompletionTokenDetails.ReasoningTokens)
}
