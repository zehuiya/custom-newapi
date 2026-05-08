package relay

import (
	"strings"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func shouldInjectSyntheticCacheInfo(channelName string, info *relaycommon.RelayInfo) bool {
	if info == nil || info.GetEstimatePromptTokens() < 4096 {
		return false
	}
	normalizedName := strings.ToLower(channelName)
	if !strings.Contains(normalizedName, "cache") {
		return false
	}
	if strings.Contains(normalizedName, "sub2api") {
		return false
	}
	if info.ChannelMeta != nil && info.ChannelOtherSettings.ClaudeInputTokensIncludesCache {
		return false
	}
	return true
}
