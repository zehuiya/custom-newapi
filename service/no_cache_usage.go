package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

func shouldNormalizeNoCacheUsage(ctx *gin.Context, relayInfo *relaycommon.RelayInfo) bool {
	if relayInfo != nil && relayInfo.ChannelMeta != nil {
		return relayInfo.ChannelSetting.NoCacheEnabled
	}
	if ctx == nil {
		return false
	}
	setting, ok := common.GetContextKeyType[dto.ChannelSettings](ctx, constant.ContextKeyChannelSetting)
	return ok && setting.NoCacheEnabled
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

// NormalizeNoCacheUsageForRelay folds cache-read tokens into regular input
// tokens when the channel's no-cache setting is enabled. It is intentionally
// idempotent.
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
