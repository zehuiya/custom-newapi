package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

const noCacheChannelMarker = "[no_cache]"

func channelNameHasNoCacheMarker(channelName string) bool {
	return strings.Contains(strings.ToLower(channelName), noCacheChannelMarker)
}

func relayChannelName(ctx *gin.Context, relayInfo *relaycommon.RelayInfo) string {
	if relayInfo != nil && relayInfo.ChannelMeta != nil && relayInfo.ChannelName != "" {
		return relayInfo.ChannelName
	}
	if ctx != nil {
		return common.GetContextKeyString(ctx, constant.ContextKeyChannelName)
	}
	return ""
}

func shouldNormalizeNoCacheUsage(ctx *gin.Context, relayInfo *relaycommon.RelayInfo) bool {
	return channelNameHasNoCacheMarker(relayChannelName(ctx, relayInfo))
}

func cacheReadTokensFromUsage(usage *dto.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.PromptTokensDetails.CachedTokens > 0 {
		return usage.PromptTokensDetails.CachedTokens
	}
	if usage.InputTokensDetails != nil && usage.InputTokensDetails.CachedTokens > 0 {
		return usage.InputTokensDetails.CachedTokens
	}
	if usage.PromptCacheHitTokens > 0 {
		return usage.PromptCacheHitTokens
	}
	return 0
}

// NormalizeNoCacheUsageForRelay folds cache-read tokens into regular input tokens
// for channels marked with [no_cache]. It is intentionally idempotent.
func NormalizeNoCacheUsageForRelay(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage) bool {
	if usage == nil || !shouldNormalizeNoCacheUsage(ctx, relayInfo) {
		return false
	}

	cacheReadTokens := cacheReadTokensFromUsage(usage)
	if cacheReadTokens <= 0 {
		return false
	}

	usage.PromptTokens += cacheReadTokens
	if usage.InputTokens > 0 {
		usage.InputTokens += cacheReadTokens
	}

	usage.PromptTokensDetails.CachedTokens = 0
	if usage.InputTokensDetails != nil {
		usage.InputTokensDetails.CachedTokens = 0
	}
	usage.PromptCacheHitTokens = 0
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return true
}
