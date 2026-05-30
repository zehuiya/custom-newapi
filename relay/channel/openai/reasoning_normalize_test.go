package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

func TestNormalizeReasoningContentInBodyAddsMessageReasoningContent(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"role":"assistant","content":"","reasoning":"hidden thought"}}]}`)

	normalizedBody, changed, err := normalizeReasoningContentInBody(body, "message")
	if err != nil {
		t.Fatalf("normalize body failed: %v", err)
	}
	if !changed {
		t.Fatal("expected body to be changed")
	}

	var normalized map[string]any
	if err := common.Unmarshal(normalizedBody, &normalized); err != nil {
		t.Fatalf("unmarshal normalized body failed: %v", err)
	}
	message := normalized["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if got := message["reasoning_content"]; got != "hidden thought" {
		t.Fatalf("expected reasoning_content to be copied, got %v", got)
	}
	if got := message["reasoning"]; got != "hidden thought" {
		t.Fatalf("expected reasoning to be preserved, got %v", got)
	}
}

func TestNormalizeReasoningContentInBodyDoesNotOverwriteExisting(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"role":"assistant","reasoning":"new","reasoning_content":"keep"}}]}`)

	normalizedBody, changed, err := normalizeReasoningContentInBody(body, "message")
	if err != nil {
		t.Fatalf("normalize body failed: %v", err)
	}
	if changed {
		t.Fatal("expected body to be unchanged")
	}

	var normalized map[string]any
	if err := common.Unmarshal(normalizedBody, &normalized); err != nil {
		t.Fatalf("unmarshal normalized body failed: %v", err)
	}
	message := normalized["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if got := message["reasoning_content"]; got != "keep" {
		t.Fatalf("expected existing reasoning_content to be preserved, got %v", got)
	}
}

func TestNormalizeReasoningContentInStreamDataAddsDeltaReasoningContent(t *testing.T) {
	data := `{"choices":[{"delta":{"reasoning":"stream thought"},"index":0,"finish_reason":null}]}`

	normalizedData, changed, err := normalizeReasoningContentInStreamData(data)
	if err != nil {
		t.Fatalf("normalize stream data failed: %v", err)
	}
	if !changed {
		t.Fatal("expected stream data to be changed")
	}

	var normalized map[string]any
	if err := common.UnmarshalJsonStr(normalizedData, &normalized); err != nil {
		t.Fatalf("unmarshal normalized stream data failed: %v", err)
	}
	delta := normalized["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
	if got := delta["reasoning_content"]; got != "stream thought" {
		t.Fatalf("expected delta reasoning_content to be copied, got %v", got)
	}
	if got := delta["reasoning"]; got != "stream thought" {
		t.Fatalf("expected delta reasoning to be preserved, got %v", got)
	}
}

func TestNormalizeReasoningContentInStreamResponseAddsDeltaReasoningContent(t *testing.T) {
	reasoning := "struct stream thought"
	response := dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					Reasoning: &reasoning,
				},
			},
		},
	}

	if !normalizeReasoningContentInStreamResponse(&response) {
		t.Fatal("expected stream response to be changed")
	}
	if response.Choices[0].Delta.ReasoningContent == nil || *response.Choices[0].Delta.ReasoningContent != reasoning {
		t.Fatalf("expected reasoning_content to be copied, got %#v", response.Choices[0].Delta.ReasoningContent)
	}
}
