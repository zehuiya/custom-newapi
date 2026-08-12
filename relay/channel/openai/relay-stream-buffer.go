package openai

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

type bufferedChatChoice struct {
	index        int
	role         string
	content      strings.Builder
	reasoning    strings.Builder
	finishReason string
	toolCalls    []*dto.ToolCallResponse
	toolIndexes  map[int]int
}

type bufferedChatAccumulator struct {
	id        string
	model     string
	created   int64
	choices   map[int]*bufferedChatChoice
	usage     *dto.Usage
	hasUsage  bool
	usageText strings.Builder
}

func newBufferedChatAccumulator() *bufferedChatAccumulator {
	return &bufferedChatAccumulator{
		choices: make(map[int]*bufferedChatChoice),
		usage:   &dto.Usage{},
	}
}

func (a *bufferedChatAccumulator) choice(index int) *bufferedChatChoice {
	if choice, ok := a.choices[index]; ok {
		return choice
	}
	choice := &bufferedChatChoice{index: index, toolIndexes: make(map[int]int)}
	a.choices[index] = choice
	return choice
}

func (a *bufferedChatAccumulator) merge(data string) (*types.OpenAIError, error) {
	var chunk struct {
		dto.ChatCompletionsStreamResponse
		Error any `json:"error"`
	}
	if err := common.UnmarshalJsonStr(data, &chunk); err != nil {
		return nil, err
	}
	if oaiErr := dto.GetOpenAIError(chunk.Error); oaiErr != nil && oaiErr.Type != "" {
		return oaiErr, nil
	}
	if a.id == "" {
		a.id = chunk.Id
	}
	if a.model == "" {
		a.model = chunk.Model
	}
	if a.created == 0 {
		a.created = chunk.Created
	}
	if service.ValidUsage(chunk.Usage) {
		a.usage = chunk.Usage
		a.hasUsage = true
	}

	for i := range chunk.Choices {
		delta := &chunk.Choices[i]
		choice := a.choice(delta.Index)
		if delta.Delta.Role != "" {
			choice.role = delta.Delta.Role
		}
		if content := delta.Delta.GetContentString(); content != "" {
			choice.content.WriteString(content)
			a.usageText.WriteString(content)
		}
		if reasoning := delta.Delta.GetReasoningContent(); reasoning != "" {
			choice.reasoning.WriteString(reasoning)
			a.usageText.WriteString(reasoning)
		}
		for j := range delta.Delta.ToolCalls {
			a.mergeToolCall(choice, &delta.Delta.ToolCalls[j])
		}
		if delta.FinishReason != nil && *delta.FinishReason != "" {
			choice.finishReason = *delta.FinishReason
		}
	}
	return nil, nil
}

func (a *bufferedChatAccumulator) mergeToolCall(choice *bufferedChatChoice, delta *dto.ToolCallResponse) {
	index := 0
	if delta.Index != nil {
		index = *delta.Index
	}
	position, ok := choice.toolIndexes[index]
	if !ok {
		choice.toolCalls = append(choice.toolCalls, &dto.ToolCallResponse{})
		position = len(choice.toolCalls) - 1
		choice.toolIndexes[index] = position
	}
	target := choice.toolCalls[position]
	if delta.ID != "" {
		target.ID = delta.ID
	}
	if delta.Type != nil {
		target.Type = delta.Type
	}
	if delta.Function.Name != "" {
		target.Function.Name = delta.Function.Name
		a.usageText.WriteString(delta.Function.Name)
	}
	if delta.Function.Description != "" {
		target.Function.Description = delta.Function.Description
	}
	if delta.Function.Arguments != "" {
		target.Function.Arguments += delta.Function.Arguments
		a.usageText.WriteString(delta.Function.Arguments)
	}
}

func (a *bufferedChatAccumulator) response(fallbackModel string) dto.OpenAITextResponse {
	indexes := make([]int, 0, len(a.choices))
	for index := range a.choices {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	choices := make([]dto.OpenAITextResponseChoice, 0, len(indexes))
	for _, index := range indexes {
		accumulated := a.choices[index]
		message := dto.Message{Role: accumulated.role}
		if message.Role == "" {
			message.Role = "assistant"
		}
		message.SetStringContent(accumulated.content.String())
		if accumulated.reasoning.Len() > 0 {
			message.ReasoningContent = accumulated.reasoning.String()
		}
		if len(accumulated.toolCalls) > 0 {
			message.SetToolCalls(accumulated.toolCalls)
		}
		choices = append(choices, dto.OpenAITextResponseChoice{
			Index:        accumulated.index,
			Message:      message,
			FinishReason: accumulated.finishReason,
		})
	}

	model := a.model
	if model == "" {
		model = fallbackModel
	}
	return dto.OpenAITextResponse{
		Id:      a.id,
		Model:   model,
		Object:  "chat.completion",
		Created: a.created,
		Choices: choices,
		Usage:   *a.usage,
	}
}

func (a *bufferedChatAccumulator) hasTerminalChoice() bool {
	if len(a.choices) == 0 {
		return false
	}
	for _, choice := range a.choices {
		if choice.finishReason == "" {
			return false
		}
	}
	return true
}

// OaiStreamBufferHandler aggregates upstream Chat Completions SSE and then
// delegates to the existing non-stream handler for conversion and billing.
func OaiStreamBufferHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	accumulator := newBufferedChatAccumulator()
	err := relaychannel.ConsumeBufferedSSE(c, resp, info, func(data string) (bool, error) {
		oaiErr, mergeErr := accumulator.merge(data)
		if mergeErr != nil {
			return false, mergeErr
		}
		if oaiErr != nil {
			return false, fmt.Errorf("upstream stream error: %s", oaiErr.Message)
		}
		return false, nil
	})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}
	if len(accumulator.choices) == 0 {
		return nil, types.NewOpenAIError(fmt.Errorf("empty stream response"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	if info.StreamStatus.EndReason == relaycommon.StreamEndReasonEOF && !accumulator.hasTerminalChoice() {
		return nil, types.NewOpenAIError(fmt.Errorf("upstream stream ended before a terminal event"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	if !accumulator.hasUsage {
		accumulator.usage = service.ResponseText2Usage(c, accumulator.usageText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
	}

	responseBody, err := common.Marshal(accumulator.response(info.UpstreamModelName))
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}
	return OpenaiHandler(c, info, relaychannel.BufferedJSONResponse(resp, responseBody))
}
