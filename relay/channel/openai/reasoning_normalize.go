package openai

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func shouldNormalizeOpenAIReasoning(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ChannelMeta != nil && info.ChannelType == constant.ChannelTypeOpenAI
}

func normalizeReasoningContentInMessageMap(message map[string]any) bool {
	reasoning, ok := message["reasoning"].(string)
	if !ok || reasoning == "" {
		return false
	}

	if value, exists := message["reasoning_content"]; exists {
		if value == nil {
			message["reasoning_content"] = reasoning
			return true
		}
		if reasoningContent, ok := value.(string); ok && reasoningContent == "" {
			message["reasoning_content"] = reasoning
			return true
		}
		return false
	}

	message["reasoning_content"] = reasoning
	return true
}

func normalizeReasoningContentInChoices(bodyMap map[string]any, fieldName string) bool {
	choices, ok := bodyMap["choices"].([]any)
	if !ok {
		return false
	}

	changed := false
	for _, choice := range choices {
		choiceMap, ok := choice.(map[string]any)
		if !ok {
			continue
		}
		message, ok := choiceMap[fieldName].(map[string]any)
		if !ok {
			continue
		}
		if normalizeReasoningContentInMessageMap(message) {
			changed = true
		}
	}
	return changed
}

func normalizeReasoningContentInBody(body []byte, fieldName string) ([]byte, bool, error) {
	var bodyMap map[string]any
	if err := common.Unmarshal(body, &bodyMap); err != nil {
		return body, false, err
	}
	if !normalizeReasoningContentInChoices(bodyMap, fieldName) {
		return body, false, nil
	}
	normalizedBody, err := common.Marshal(bodyMap)
	if err != nil {
		return body, false, err
	}
	return normalizedBody, true, nil
}

func normalizeReasoningContentInStreamData(data string) (string, bool, error) {
	normalizedBody, changed, err := normalizeReasoningContentInBody(common.StringToByteSlice(data), "delta")
	if err != nil || !changed {
		return data, changed, err
	}
	return string(normalizedBody), true, nil
}

func normalizeReasoningContentInTextResponse(response *dto.OpenAITextResponse) bool {
	if response == nil {
		return false
	}

	changed := false
	for i := range response.Choices {
		message := &response.Choices[i].Message
		if message.Reasoning != "" && message.ReasoningContent == "" {
			message.ReasoningContent = message.Reasoning
			changed = true
		}
	}
	return changed
}

func normalizeReasoningContentInStreamResponse(response *dto.ChatCompletionsStreamResponse) bool {
	if response == nil {
		return false
	}

	changed := false
	for i := range response.Choices {
		delta := &response.Choices[i].Delta
		if delta.Reasoning == nil || *delta.Reasoning == "" {
			continue
		}
		if delta.ReasoningContent != nil && *delta.ReasoningContent != "" {
			continue
		}
		reasoning := *delta.Reasoning
		delta.ReasoningContent = &reasoning
		changed = true
	}
	return changed
}

func collectReasoningAndOutputContentFromOpenAIResponse(response *dto.OpenAITextResponse) (string, string) {
	if response == nil {
		return "", ""
	}

	var reasoningText strings.Builder
	var outputText strings.Builder
	for _, choice := range response.Choices {
		outputText.WriteString(choice.Message.StringContent())
		if len(choice.Message.ToolCalls) > 0 {
			outputText.Write(choice.Message.ToolCalls)
		}
		if choice.Message.ReasoningContent != "" {
			reasoningText.WriteString(choice.Message.ReasoningContent)
			continue
		}
		if choice.Message.Reasoning != "" {
			reasoningText.WriteString(choice.Message.Reasoning)
		}
	}
	return reasoningText.String(), outputText.String()
}

func collectReasoningAndOutputContentFromOpenAIStreamItems(streamItems []string) (string, string) {
	var reasoningText strings.Builder
	var outputText strings.Builder
	for _, item := range streamItems {
		var streamResponse dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(item, &streamResponse); err != nil {
			continue
		}
		for _, choice := range streamResponse.Choices {
			reasoningText.WriteString(choice.Delta.GetReasoningContent())
			outputText.WriteString(choice.Delta.GetContentString())
			for _, tool := range choice.Delta.ToolCalls {
				outputText.WriteString(tool.Function.Name)
				outputText.WriteString(tool.Function.Arguments)
			}
		}
	}
	return reasoningText.String(), outputText.String()
}
