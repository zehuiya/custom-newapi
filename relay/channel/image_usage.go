package channel

import "github.com/QuantumNous/new-api/dto"

func BuildImageResponseUsage() *dto.Usage {
	return &dto.Usage{
		PromptTokens: 1,
		TotalTokens:  1,
		InputTokens:  1,
		UsageSource:  "new-api-image",
	}
}
