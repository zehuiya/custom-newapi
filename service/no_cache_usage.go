package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

func cacheUsageChannelSetting(ctx *gin.Context, relayInfo *relaycommon.RelayInfo) (dto.ChannelSettings, bool) {
	if relayInfo != nil && relayInfo.ChannelMeta != nil {
		return relayInfo.ChannelSetting, true
	}
	if ctx == nil {
		return dto.ChannelSettings{}, false
	}
	setting, ok := common.GetContextKeyType[dto.ChannelSettings](ctx, constant.ContextKeyChannelSetting)
	return setting, ok
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

func setCacheReadTokensOnUsage(usage *dto.Usage, cacheReadTokens int) {
	usage.PromptTokensDetails.CachedTokens = cacheReadTokens
	if usage.InputTokensDetails != nil {
		usage.InputTokensDetails.CachedTokens = cacheReadTokens
	}
	if usage.PromptCacheHitTokens > 0 {
		usage.PromptCacheHitTokens = cacheReadTokens
	}
}

func reduceCacheUsage(usage *dto.Usage, percentage int) bool {
	if percentage <= 0 || percentage > 100 {
		return false
	}

	cacheReadTokens := cacheReadTokensFromUsage(usage)
	state := usage.CacheReductionState
	if state != nil && state.Percentage == percentage {
		promptUnchanged := usage.PromptTokens == state.AdjustedPromptTokens
		inputUnchanged := usage.InputTokens == state.AdjustedInputTokens
		cacheUnchanged := cacheReadTokens == state.AdjustedCacheReadTokens
		if promptUnchanged && inputUnchanged && cacheUnchanged {
			return false
		}

		// Streaming providers may refresh only one part of usage in a later
		// event. Restore any still-adjusted fields before applying the new view.
		if promptUnchanged {
			usage.PromptTokens = state.OriginalPromptTokens
		}
		if inputUnchanged {
			usage.InputTokens = state.OriginalInputTokens
		}
		if cacheUnchanged {
			cacheReadTokens = state.OriginalCacheReadTokens
			setCacheReadTokensOnUsage(usage, cacheReadTokens)
		}
	}

	if cacheReadTokens <= 0 {
		return false
	}
	reducedTokens := cacheReadTokens * percentage / 100
	if reducedTokens <= 0 {
		return false
	}

	originalPromptTokens := usage.PromptTokens
	originalInputTokens := usage.InputTokens
	usage.PromptTokens += reducedTokens
	if usage.InputTokens > 0 {
		usage.InputTokens += reducedTokens
	}
	remainingCacheTokens := cacheReadTokens - reducedTokens
	setCacheReadTokensOnUsage(usage, remainingCacheTokens)
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	usage.CacheReductionState = &dto.CacheReductionUsageState{
		Percentage:              percentage,
		OriginalPromptTokens:    originalPromptTokens,
		OriginalInputTokens:     originalInputTokens,
		OriginalCacheReadTokens: cacheReadTokens,
		AdjustedPromptTokens:    usage.PromptTokens,
		AdjustedInputTokens:     usage.InputTokens,
		AdjustedCacheReadTokens: remainingCacheTokens,
	}
	return true
}

// AdjustCacheUsageForRelay applies the channel's configured cache accounting
// mode to response usage. Cache injection happens earlier in the relay path.
func AdjustCacheUsageForRelay(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage) bool {
	if usage == nil {
		return false
	}
	setting, ok := cacheUsageChannelSetting(ctx, relayInfo)
	if !ok {
		return false
	}

	if setting.CacheReductionEnabled {
		return reduceCacheUsage(usage, setting.GetCacheReductionPercentage())
	}
	if !setting.NoCacheEnabled {
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

// NormalizeNoCacheUsageForRelay folds cache-read tokens into regular input
// tokens when no-cache is enabled. It remains as a compatibility entry point;
// percentage-based cache reduction uses the same established relay call sites.
func NormalizeNoCacheUsageForRelay(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage) bool {
	return AdjustCacheUsageForRelay(ctx, relayInfo, usage)
}
