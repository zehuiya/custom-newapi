package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestChannelSettingsShouldInjectCache(t *testing.T) {
	tests := []struct {
		name                  string
		setting               dto.ChannelSettings
		estimatedPromptTokens int
		want                  bool
	}{
		{
			name:                  "disabled above threshold",
			setting:               dto.ChannelSettings{},
			estimatedPromptTokens: 5000,
			want:                  false,
		},
		{
			name:                  "enabled below threshold",
			setting:               dto.ChannelSettings{CacheEnabled: true},
			estimatedPromptTokens: 4095,
			want:                  false,
		},
		{
			name:                  "enabled at threshold",
			setting:               dto.ChannelSettings{CacheEnabled: true},
			estimatedPromptTokens: 4096,
			want:                  true,
		},
		{
			name:                  "enabled above threshold",
			setting:               dto.ChannelSettings{CacheEnabled: true},
			estimatedPromptTokens: 10000,
			want:                  true,
		},
		{
			name: "no cache takes precedence defensively",
			setting: dto.ChannelSettings{
				CacheEnabled:   true,
				NoCacheEnabled: true,
			},
			estimatedPromptTokens: 5000,
			want:                  false,
		},
		{
			name: "cache reduction takes precedence defensively",
			setting: dto.ChannelSettings{
				CacheEnabled:          true,
				CacheReductionEnabled: true,
			},
			estimatedPromptTokens: 5000,
			want:                  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.setting.ShouldInjectCache(tt.estimatedPromptTokens))
		})
	}
}
