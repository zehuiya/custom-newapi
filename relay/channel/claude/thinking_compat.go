package claude

import (
	"strings"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

const deepSeekThinkingPlaceholder = "...[truncated]"

// patchDeepSeekThinkingHistory works around DeepSeek's Anthropic-compatible
// endpoint requiring a thinking block to be replayed for assistant history,
// even when the model did not return one. This synthetic block is intentionally
// limited to the affected DeepSeek models: official Claude thinking blocks are
// signed and must be passed back unchanged.
func patchDeepSeekThinkingHistory(info *relaycommon.RelayInfo, request *dto.ClaudeRequest) {
	if request == nil || !isDeepSeekThinkingPassbackModel(finalClaudeModelName(info, request)) {
		return
	}

	for i := range request.Messages {
		message := &request.Messages[i]
		if message.Role != "assistant" {
			continue
		}
		message.Content = ensureThinkingBlock(message.Content)
	}
}

func finalClaudeModelName(info *relaycommon.RelayInfo, request *dto.ClaudeRequest) string {
	if info != nil && info.ChannelMeta != nil && info.UpstreamModelName != "" {
		return info.UpstreamModelName
	}
	if request != nil && request.Model != "" {
		return request.Model
	}
	if info != nil {
		return info.OriginModelName
	}
	return ""
}

func isDeepSeekThinkingPassbackModel(model string) bool {
	model = strings.TrimSpace(model)
	model = strings.TrimPrefix(model, "anthropic:")
	for _, family := range []string{"deepseek-v4-flash", "deepseek-v4-pro"} {
		if model == family || strings.HasPrefix(model, family+"-") {
			return true
		}
	}
	return false
}

func ensureThinkingBlock(content any) any {
	switch blocks := content.(type) {
	case string:
		patched := []any{newThinkingBlock()}
		if blocks != "" {
			patched = append(patched, map[string]any{
				"type": "text",
				"text": blocks,
			})
		}
		return patched
	case nil:
		return []any{newThinkingBlock()}
	case []any:
		if anyBlockHasType(blocks, "thinking") {
			return content
		}
		return append([]any{newThinkingBlock()}, blocks...)
	case []map[string]any:
		if mapBlocksHaveType(blocks, "thinking") {
			return content
		}
		return append([]map[string]any{newThinkingBlock()}, blocks...)
	case []dto.ClaudeMediaMessage:
		if mediaBlocksHaveType(blocks, "thinking") {
			return content
		}
		placeholder := deepSeekThinkingPlaceholder
		return append([]dto.ClaudeMediaMessage{{
			Type:     "thinking",
			Thinking: &placeholder,
		}}, blocks...)
	case []*dto.ClaudeMediaMessage:
		if mediaPointerBlocksHaveType(blocks, "thinking") {
			return content
		}
		placeholder := deepSeekThinkingPlaceholder
		return append([]*dto.ClaudeMediaMessage{{
			Type:     "thinking",
			Thinking: &placeholder,
		}}, blocks...)
	default:
		return content
	}
}

func newThinkingBlock() map[string]any {
	return map[string]any{
		"type":     "thinking",
		"thinking": deepSeekThinkingPlaceholder,
	}
}

func anyBlockHasType(blocks []any, targetType string) bool {
	for _, block := range blocks {
		switch value := block.(type) {
		case map[string]any:
			if value["type"] == targetType {
				return true
			}
		case dto.ClaudeMediaMessage:
			if value.Type == targetType {
				return true
			}
		case *dto.ClaudeMediaMessage:
			if value != nil && value.Type == targetType {
				return true
			}
		}
	}
	return false
}

func mapBlocksHaveType(blocks []map[string]any, targetType string) bool {
	for _, block := range blocks {
		if block["type"] == targetType {
			return true
		}
	}
	return false
}

func mediaBlocksHaveType(blocks []dto.ClaudeMediaMessage, targetType string) bool {
	for _, block := range blocks {
		if block.Type == targetType {
			return true
		}
	}
	return false
}

func mediaPointerBlocksHaveType(blocks []*dto.ClaudeMediaMessage, targetType string) bool {
	for _, block := range blocks {
		if block != nil && block.Type == targetType {
			return true
		}
	}
	return false
}
