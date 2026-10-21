package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestChannelSettingsEndpointDefaultsAndIndependentOverrides(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		response bool
		messages bool
		chat     bool
	}{
		{name: "legacy", raw: `{}`, response: true, messages: true, chat: true},
		{name: "null means default", raw: `{"responses_enabled":null,"messages_enabled":null,"chat_completions_enabled":null}`, response: true, messages: true, chat: true},
		{name: "explicitly enabled", raw: `{"responses_enabled":true,"messages_enabled":true,"chat_completions_enabled":true}`, response: true, messages: true, chat: true},
		{name: "responses disabled", raw: `{"responses_enabled":false}`, response: false, messages: true, chat: true},
		{name: "messages disabled", raw: `{"messages_enabled":false}`, response: true, messages: false, chat: true},
		{name: "chat disabled", raw: `{"chat_completions_enabled":false}`, response: true, messages: true, chat: false},
		{name: "all disabled", raw: `{"responses_enabled":false,"messages_enabled":false,"chat_completions_enabled":false}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var settings ChannelSettings
			require.NoError(t, common.UnmarshalJsonStr(tt.raw, &settings))
			require.Equal(t, tt.response, settings.SupportsResponses())
			require.Equal(t, tt.messages, settings.SupportsMessages())
			require.Equal(t, tt.chat, settings.SupportsChatCompletions())
			for _, path := range []string{"/v1/responses", "/v1/messages", "/v1/chat/completions", "/pg/chat/completions"} {
				require.True(t, IsChannelEndpointPath(path), path)
			}
			require.Equal(t, tt.response, settings.SupportsRequestPath("/v1/responses"))
			require.Equal(t, tt.messages, settings.SupportsRequestPath("/v1/messages"))
			require.Equal(t, tt.chat, settings.SupportsRequestPath("/v1/chat/completions"))
			require.Equal(t, tt.chat, settings.SupportsRequestPath("/pg/chat/completions"))
			for _, path := range []string{"", "/v1/responses/compact", "/v1/completions", "/v1/embeddings", "/v1/realtime", "/v1beta/models/example:generateContent"} {
				require.False(t, IsChannelEndpointPath(path), path)
				require.True(t, settings.SupportsRequestPath(path), path)
			}
		})
	}
}

func TestChannelSettingsPreserveExplicitFalseOnMarshal(t *testing.T) {
	var settings ChannelSettings
	require.NoError(t, common.UnmarshalJsonStr(`{"responses_enabled":false,"messages_enabled":false,"chat_completions_enabled":false}`, &settings))
	encoded, err := common.Marshal(settings)
	require.NoError(t, err)
	var fields map[string]interface{}
	require.NoError(t, common.Unmarshal(encoded, &fields))
	for _, key := range []string{"responses_enabled", "messages_enabled", "chat_completions_enabled"} {
		require.Contains(t, fields, key)
		require.Equal(t, false, fields[key])
	}

	encoded, err = common.Marshal(ChannelSettings{})
	require.NoError(t, err)
	fields = nil
	require.NoError(t, common.Unmarshal(encoded, &fields))
	for _, key := range []string{"responses_enabled", "messages_enabled", "chat_completions_enabled"} {
		require.NotContains(t, fields, key)
	}
}
