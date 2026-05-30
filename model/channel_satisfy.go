package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

func IsChannelEnabledForGroupModel(group string, modelName string, channelID int) bool {
	if group == "" || modelName == "" || channelID <= 0 {
		return false
	}
	if !common.MemoryCacheEnabled {
		return isChannelEnabledForGroupModelDB(group, modelName, channelID)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	if group2model2channels == nil {
		return false
	}

	if isChannelIDInList(group2model2channels[group][modelName], channelID) {
		return true
	}
	normalized := ratio_setting.FormatMatchingModelName(modelName)
	if normalized != "" && normalized != modelName {
		return isChannelIDInList(group2model2channels[group][normalized], channelID)
	}
	return false
}

func IsChannelEnabledForAnyGroupModel(groups []string, modelName string, channelID int) bool {
	if len(groups) == 0 {
		return false
	}
	for _, g := range groups {
		if IsChannelEnabledForGroupModel(g, modelName, channelID) {
			return true
		}
	}
	return false
}

func HasContextLimitedChannelForGroupModel(group string, modelName string) bool {
	hasContextLimit, _ := TokenLimitedChannelFlagsForGroupModel(group, modelName)
	return hasContextLimit
}

func TokenLimitedChannelFlagsForGroupModel(group string, modelName string) (bool, bool) {
	if group == "" || modelName == "" {
		return false, false
	}
	if !common.MemoryCacheEnabled {
		return tokenLimitedChannelFlagsForGroupModelDB(group, modelName)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	if group2model2channels == nil {
		return false, false
	}

	seen := make(map[int]struct{})
	hasContextLimit, hasOutputLimit := tokenLimitedChannelFlagsInList(group2model2channels[group][modelName], seen)
	if hasContextLimit && hasOutputLimit {
		return true, true
	}
	normalized := ratio_setting.FormatMatchingModelName(modelName)
	if normalized != "" && normalized != modelName {
		normalizedHasContextLimit, normalizedHasOutputLimit := tokenLimitedChannelFlagsInList(group2model2channels[group][normalized], seen)
		hasContextLimit = hasContextLimit || normalizedHasContextLimit
		hasOutputLimit = hasOutputLimit || normalizedHasOutputLimit
	}
	return hasContextLimit, hasOutputLimit
}

func isChannelEnabledForGroupModelDB(group string, modelName string, channelID int) bool {
	var count int64
	err := DB.Model(&Ability{}).
		Where(commonGroupCol+" = ? and model = ? and channel_id = ? and enabled = ?", group, modelName, channelID, true).
		Count(&count).Error
	if err == nil && count > 0 {
		return true
	}
	normalized := ratio_setting.FormatMatchingModelName(modelName)
	if normalized == "" || normalized == modelName {
		return false
	}
	count = 0
	err = DB.Model(&Ability{}).
		Where(commonGroupCol+" = ? and model = ? and channel_id = ? and enabled = ?", group, normalized, channelID, true).
		Count(&count).Error
	return err == nil && count > 0
}

func hasContextLimitedChannelForGroupModelDB(group string, modelName string) bool {
	hasContextLimit, _ := tokenLimitedChannelFlagsForGroupModelDB(group, modelName)
	return hasContextLimit
}

func tokenLimitedChannelFlagsForGroupModelDB(group string, modelName string) (bool, bool) {
	hasContextLimit, hasOutputLimit := tokenLimitedChannelFlagsForGroupModelDBExact(group, modelName)
	if hasContextLimit && hasOutputLimit {
		return true, true
	}
	normalized := ratio_setting.FormatMatchingModelName(modelName)
	if normalized != "" && normalized != modelName {
		normalizedHasContextLimit, normalizedHasOutputLimit := tokenLimitedChannelFlagsForGroupModelDBExact(group, normalized)
		hasContextLimit = hasContextLimit || normalizedHasContextLimit
		hasOutputLimit = hasOutputLimit || normalizedHasOutputLimit
	}
	return hasContextLimit, hasOutputLimit
}

func tokenLimitedChannelFlagsForGroupModelDBExact(group string, modelName string) (bool, bool) {
	var channels []Channel
	groupCol := "abilities." + commonGroupCol
	err := DB.Table("abilities").
		Select("channels.max_context_tokens, channels.max_output_tokens").
		Joins("JOIN channels ON channels.id = abilities.channel_id").
		Where(groupCol+" = ? and abilities.model = ? and abilities.enabled = ?", group, modelName, true).
		Scan(&channels).Error
	if err != nil {
		return false, false
	}
	hasContextLimit := false
	hasOutputLimit := false
	for i := range channels {
		hasContextLimit = hasContextLimit || channels[i].GetMaxContextTokens() > 0
		hasOutputLimit = hasOutputLimit || channels[i].GetMaxOutputTokens() > 0
		if hasContextLimit && hasOutputLimit {
			return true, true
		}
	}
	return hasContextLimit, hasOutputLimit
}

func isChannelIDInList(list []int, channelID int) bool {
	for _, id := range list {
		if id == channelID {
			return true
		}
	}
	return false
}

func hasContextLimitedChannelInList(list []int, seen map[int]struct{}) bool {
	hasContextLimit, _ := tokenLimitedChannelFlagsInList(list, seen)
	return hasContextLimit
}

func tokenLimitedChannelFlagsInList(list []int, seen map[int]struct{}) (bool, bool) {
	hasContextLimit := false
	hasOutputLimit := false
	for _, id := range list {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if channel, ok := channelsIDM[id]; ok {
			hasContextLimit = hasContextLimit || channel.GetMaxContextTokens() > 0
			hasOutputLimit = hasOutputLimit || channel.GetMaxOutputTokens() > 0
			if hasContextLimit && hasOutputLimit {
				return true, true
			}
		}
	}
	return hasContextLimit, hasOutputLimit
}
