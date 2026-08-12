package claude

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

type bufferedClaudeBlock struct {
	value       dto.ClaudeMediaMessage
	text        strings.Builder
	thinking    strings.Builder
	partialJSON strings.Builder
	signature   strings.Builder
}

type bufferedClaudeAccumulator struct {
	id         string
	model      string
	role       string
	stopReason string
	usage      *dto.ClaudeUsage
	blocks     map[int]*bufferedClaudeBlock
}

func newBufferedClaudeAccumulator() *bufferedClaudeAccumulator {
	return &bufferedClaudeAccumulator{
		role:   "assistant",
		usage:  &dto.ClaudeUsage{},
		blocks: make(map[int]*bufferedClaudeBlock),
	}
}

func (a *bufferedClaudeAccumulator) block(index int) *bufferedClaudeBlock {
	if block, ok := a.blocks[index]; ok {
		return block
	}
	block := &bufferedClaudeBlock{}
	a.blocks[index] = block
	return block
}

func (a *bufferedClaudeAccumulator) merge(data string) (bool, error) {
	var event dto.ClaudeResponse
	if err := common.UnmarshalJsonStr(data, &event); err != nil {
		return false, err
	}
	if claudeErr := event.GetClaudeError(); claudeErr != nil && claudeErr.Type != "" {
		return false, fmt.Errorf("upstream Claude stream error: %s", claudeErr.Message)
	}

	switch event.Type {
	case "message_start":
		if event.Message != nil {
			a.id = event.Message.Id
			a.model = event.Message.Model
			if event.Message.Role != "" {
				a.role = event.Message.Role
			}
			mergeBufferedClaudeUsage(a.usage, event.Message.Usage)
		}
	case "content_block_start":
		if event.ContentBlock != nil {
			block := a.block(event.GetIndex())
			block.value = *event.ContentBlock
			if event.ContentBlock.Text != nil {
				block.text.WriteString(*event.ContentBlock.Text)
			}
			if event.ContentBlock.Thinking != nil {
				block.thinking.WriteString(*event.ContentBlock.Thinking)
			}
			if event.ContentBlock.Signature != "" {
				block.signature.WriteString(event.ContentBlock.Signature)
			}
		}
	case "content_block_delta":
		if event.Delta == nil {
			break
		}
		block := a.block(event.GetIndex())
		if block.value.Type == "" {
			switch event.Delta.Type {
			case "thinking_delta", "signature_delta":
				block.value.Type = "thinking"
			case "input_json_delta":
				block.value.Type = "tool_use"
			default:
				block.value.Type = "text"
			}
		}
		if event.Delta.Text != nil {
			block.text.WriteString(*event.Delta.Text)
		}
		if event.Delta.Thinking != nil {
			block.thinking.WriteString(*event.Delta.Thinking)
		}
		if event.Delta.PartialJson != nil {
			block.partialJSON.WriteString(*event.Delta.PartialJson)
		}
		if event.Delta.Signature != "" {
			block.signature.WriteString(event.Delta.Signature)
		}
	case "message_delta":
		if event.Delta != nil && event.Delta.StopReason != nil {
			a.stopReason = *event.Delta.StopReason
		}
		mergeBufferedClaudeUsage(a.usage, event.Usage)
	case "message_stop":
		return true, nil
	}
	return false, nil
}

func mergeBufferedClaudeUsage(target *dto.ClaudeUsage, source *dto.ClaudeUsage) {
	if target == nil || source == nil {
		return
	}
	if source.InputTokens != 0 {
		target.InputTokens = source.InputTokens
	}
	if source.OutputTokens != 0 {
		target.OutputTokens = source.OutputTokens
	}
	if source.CacheCreationInputTokens != 0 {
		target.CacheCreationInputTokens = source.CacheCreationInputTokens
	}
	if source.CacheReadInputTokens != 0 {
		target.CacheReadInputTokens = source.CacheReadInputTokens
	}
	if source.CacheCreation != nil {
		copyValue := *source.CacheCreation
		target.CacheCreation = &copyValue
	}
	if source.ClaudeCacheCreation5mTokens != 0 {
		target.ClaudeCacheCreation5mTokens = source.ClaudeCacheCreation5mTokens
	}
	if source.ClaudeCacheCreation1hTokens != 0 {
		target.ClaudeCacheCreation1hTokens = source.ClaudeCacheCreation1hTokens
	}
	if source.ServerToolUse != nil {
		copyValue := *source.ServerToolUse
		target.ServerToolUse = &copyValue
	}
}

func (a *bufferedClaudeAccumulator) response(fallbackModel string) (*dto.ClaudeResponse, error) {
	indexes := make([]int, 0, len(a.blocks))
	for index := range a.blocks {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	content := make([]dto.ClaudeMediaMessage, 0, len(indexes))
	for _, index := range indexes {
		block := a.blocks[index]
		value := block.value
		switch value.Type {
		case "thinking":
			thinking := block.thinking.String()
			value.Thinking = &thinking
			if block.signature.Len() > 0 {
				value.Signature = block.signature.String()
			}
		case "tool_use":
			if block.partialJSON.Len() > 0 {
				var input any
				if err := common.Unmarshal(common.StringToByteSlice(block.partialJSON.String()), &input); err != nil {
					return nil, fmt.Errorf("invalid streamed tool input: %w", err)
				}
				value.Input = input
			}
		default:
			text := block.text.String()
			value.Text = &text
		}
		content = append(content, value)
	}

	model := a.model
	if model == "" {
		model = fallbackModel
	}
	return &dto.ClaudeResponse{
		Id:         a.id,
		Type:       "message",
		Role:       a.role,
		Content:    content,
		StopReason: a.stopReason,
		Model:      model,
		Usage:      a.usage,
	}, nil
}

// ClaudeStreamBufferHandler aggregates Anthropic SSE and delegates final
// conversion, cache normalization and token accounting to ClaudeHandler.
func ClaudeStreamBufferHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	accumulator := newBufferedClaudeAccumulator()
	err := relaychannel.ConsumeBufferedSSE(c, resp, info, accumulator.merge)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}
	if len(accumulator.blocks) == 0 {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("empty Claude stream response"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	if info.StreamStatus.EndReason == relaycommon.StreamEndReasonEOF && accumulator.stopReason == "" {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("upstream Claude stream ended before a terminal event"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	response, err := accumulator.response(info.UpstreamModelName)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}
	responseBody, err := common.Marshal(response)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeJsonMarshalFailed)
	}
	return ClaudeHandler(c, relaychannel.BufferedJSONResponse(resp, responseBody), info)
}
