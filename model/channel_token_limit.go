package model

import "github.com/QuantumNous/new-api/dto"

type ChannelTokenLimit struct {
	InputTokens int
	MaxTokens   int
}

type ChannelSelectionConstraint struct {
	TokenLimit  *ChannelTokenLimit
	RequestPath string
}

func (limit *ChannelTokenLimit) Satisfies(channel *Channel) bool {
	if limit == nil || channel == nil {
		return true
	}
	if maxOutputTokens := channel.GetMaxOutputTokens(); maxOutputTokens > 0 && limit.MaxTokens > maxOutputTokens {
		return false
	}
	if minInputTokens := channel.GetMinInputTokens(); minInputTokens > 0 && limit.InputTokens < minInputTokens {
		return false
	}
	if maxInputTokens := channel.GetMaxInputTokens(); maxInputTokens > 0 && limit.InputTokens > maxInputTokens {
		return false
	}
	if maxContextTokens := channel.GetMaxContextTokens(); maxContextTokens > 0 && limit.InputTokens+limit.MaxTokens >= maxContextTokens {
		return false
	}
	return true
}

func (constraint *ChannelSelectionConstraint) Satisfies(channel *Channel) bool {
	if constraint == nil || channel == nil {
		return true
	}
	if !constraint.TokenLimit.Satisfies(channel) {
		return false
	}
	if dto.IsChannelEndpointPath(constraint.RequestPath) && !channel.GetSetting().SupportsRequestPath(constraint.RequestPath) {
		return false
	}
	return true
}
