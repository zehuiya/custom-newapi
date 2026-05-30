package service

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestResponseOpenAI2ClaudeIncludesThinkingFromReasoning(t *testing.T) {
	openAIResponse := &dto.OpenAITextResponse{
		Model: "mock-model",
		Choices: []dto.OpenAITextResponseChoice{
			{
				Message: dto.Message{
					Role:      "assistant",
					Content:   "visible answer",
					Reasoning: "hidden thought",
				},
				FinishReason: "stop",
			},
		},
	}

	claudeResponse := ResponseOpenAI2Claude(openAIResponse, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
	})
	if len(claudeResponse.Content) != 2 {
		t.Fatalf("expected thinking and text content blocks, got %d", len(claudeResponse.Content))
	}
	if claudeResponse.Content[0].Type != "thinking" {
		t.Fatalf("expected first block to be thinking, got %q", claudeResponse.Content[0].Type)
	}
	if claudeResponse.Content[0].Thinking == nil || *claudeResponse.Content[0].Thinking != "hidden thought" {
		t.Fatalf("expected thinking to be copied, got %#v", claudeResponse.Content[0].Thinking)
	}
	if claudeResponse.Content[1].Type != "text" || claudeResponse.Content[1].GetText() != "visible answer" {
		t.Fatalf("expected second block to be text answer, got %#v", claudeResponse.Content[1])
	}
}

func TestResponseOpenAI2ClaudeDoesNotAddThinkingForNonOpenAIChannel(t *testing.T) {
	openAIResponse := &dto.OpenAITextResponse{
		Model: "mock-model",
		Choices: []dto.OpenAITextResponseChoice{
			{
				Message: dto.Message{
					Role:      "assistant",
					Content:   "visible answer",
					Reasoning: "hidden thought",
				},
				FinishReason: "stop",
			},
		},
	}

	claudeResponse := ResponseOpenAI2Claude(openAIResponse, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeGemini},
	})
	if len(claudeResponse.Content) != 1 {
		t.Fatalf("expected only text content block, got %d", len(claudeResponse.Content))
	}
	if claudeResponse.Content[0].Type != "text" || claudeResponse.Content[0].GetText() != "visible answer" {
		t.Fatalf("expected text answer to be preserved, got %#v", claudeResponse.Content[0])
	}
}

func TestStreamResponseOpenAI2ClaudeUsesReasoningField(t *testing.T) {
	reasoning := "stream hidden thought"
	streamResponse := &dto.ChatCompletionsStreamResponse{
		Id:    "chatcmpl-test",
		Model: "mock-model",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Index: 0,
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					Reasoning: &reasoning,
				},
			},
		},
	}
	info := &relaycommon.RelayInfo{
		SendResponseCount: 1,
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{
			LastMessagesType: relaycommon.LastMessageTypeNone,
		},
	}

	claudeResponses := StreamResponseOpenAI2Claude(streamResponse, info)
	foundThinkingDelta := false
	for _, response := range claudeResponses {
		if response.Delta != nil && response.Delta.Thinking != nil && *response.Delta.Thinking == reasoning {
			foundThinkingDelta = true
			break
		}
	}
	if !foundThinkingDelta {
		t.Fatalf("expected Claude stream conversion to emit thinking_delta, got %#v", claudeResponses)
	}
}
