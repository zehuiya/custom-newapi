package common

import (
	"math/rand"

	"github.com/QuantumNous/new-api/dto"
)

func sampleSyntheticCachePercentage(setting dto.ChannelSettings) int {
	minPercentage, maxPercentage := setting.GetCachePercentageRange()
	if minPercentage < 0 || maxPercentage > 100 || minPercentage > maxPercentage {
		minPercentage = dto.DefaultCachePercentageMin
		maxPercentage = dto.DefaultCachePercentageMax
	}

	percentage := minPercentage
	if maxPercentage > minPercentage {
		percentage += rand.Intn(maxPercentage - minPercentage + 1)
	}
	return percentage
}

func syntheticCacheTokensAtPercentage(totalPromptTokens int, percentage int) int {
	if totalPromptTokens <= 0 {
		return 0
	}
	return int(int64(totalPromptTokens) * int64(percentage) / 100)
}

// CalculateSyntheticCacheTokens returns a random cached-token count within the
// channel's configured inclusive percentage range. Invalid persisted ranges
// fall back to the historical 50%-90% defaults so relay processing remains
// safe even if settings were edited outside the API.
func CalculateSyntheticCacheTokens(totalPromptTokens int, setting dto.ChannelSettings) int {
	if totalPromptTokens <= 0 {
		return 0
	}
	return syntheticCacheTokensAtPercentage(totalPromptTokens, sampleSyntheticCachePercentage(setting))
}

// CalculateSyntheticCacheTokens samples once per upstream attempt so a valid
// 0% result cannot be mistaken for "not injected" and re-rolled by a later
// streaming/final-response stage.
func (info *RelayInfo) CalculateSyntheticCacheTokens(totalPromptTokens int) int {
	if totalPromptTokens <= 0 {
		return 0
	}
	if info == nil {
		return CalculateSyntheticCacheTokens(totalPromptTokens, dto.ChannelSettings{})
	}
	if info.syntheticCachePercent == nil {
		percentage := sampleSyntheticCachePercentage(info.ChannelSetting)
		info.syntheticCachePercent = &percentage
	}
	return syntheticCacheTokensAtPercentage(totalPromptTokens, *info.syntheticCachePercent)
}

// CalculateSyntheticCacheTarget returns the larger of the upstream cache and
// the configured synthetic target. Existing cache is only eligible for an
// increase when cache_override_enabled is set; this preserves legacy behavior.
func (info *RelayInfo) CalculateSyntheticCacheTarget(totalInputTokens int, upstreamCachedTokens int) (int, bool) {
	if info == nil || totalInputTokens <= 0 || upstreamCachedTokens < 0 {
		return upstreamCachedTokens, false
	}
	if upstreamCachedTokens > 0 && !info.ChannelSetting.CacheOverrideEnabled {
		return upstreamCachedTokens, false
	}

	targetCachedTokens := info.CalculateSyntheticCacheTokens(totalInputTokens)
	if targetCachedTokens <= upstreamCachedTokens {
		return upstreamCachedTokens, false
	}
	return targetCachedTokens, true
}
