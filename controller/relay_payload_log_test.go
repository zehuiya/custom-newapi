package controller

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service/relaypayloadlog"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestShouldCaptureRelayPayload(t *testing.T) {
	tests := []struct {
		name        string
		relayFormat types.RelayFormat
		relayMode   int
		want        bool
	}{
		{name: "anthropic messages", relayFormat: types.RelayFormatClaude, relayMode: relayconstant.RelayModeChatCompletions, want: true},
		{name: "openai chat completions", relayFormat: types.RelayFormatOpenAI, relayMode: relayconstant.RelayModeChatCompletions, want: true},
		{name: "openai completions", relayFormat: types.RelayFormatOpenAI, relayMode: relayconstant.RelayModeCompletions, want: true},
		{name: "openai unsupported mode", relayFormat: types.RelayFormatOpenAI, relayMode: relayconstant.RelayModeEmbeddings, want: false},
		{name: "openai responses", relayFormat: types.RelayFormatOpenAIResponses, relayMode: relayconstant.RelayModeResponses, want: true},
		{name: "responses format with wrong mode", relayFormat: types.RelayFormatOpenAIResponses, relayMode: relayconstant.RelayModeChatCompletions, want: false},
		{name: "responses compaction remains excluded", relayFormat: types.RelayFormatOpenAIResponsesCompaction, relayMode: relayconstant.RelayModeResponsesCompact, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{RelayMode: tt.relayMode}
			require.Equal(t, tt.want, shouldCaptureRelayPayload(tt.relayFormat, info))
		})
	}

	require.False(t, shouldCaptureRelayPayload(types.RelayFormatOpenAIResponses, nil))
}

func TestRelayPayloadLogProtocol(t *testing.T) {
	require.Equal(t, relaypayloadlog.ProtocolAnthropic, relayPayloadLogProtocol(types.RelayFormatClaude))
	require.Equal(t, relaypayloadlog.ProtocolOpenAI, relayPayloadLogProtocol(types.RelayFormatOpenAI))
	require.Equal(t, relaypayloadlog.ProtocolOpenAI, relayPayloadLogProtocol(types.RelayFormatOpenAIResponses))
	require.Empty(t, relayPayloadLogProtocol(types.RelayFormatGemini))
}
