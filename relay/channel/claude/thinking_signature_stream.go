package claude

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
)

// ensureClaudeStreamResponseID keeps the generated ID visible in message_start
// so clients receive the same value later used for missing thinking signatures.
func ensureClaudeStreamResponseID(data string, event *dto.ClaudeResponse) (string, error) {
	if event == nil || event.Type != "message_start" || event.Message == nil || event.Message.Id != "" {
		return data, nil
	}
	id := newClaudeResponseID()
	patchedData, err := sjson.Set(data, "message.id", id)
	if err != nil {
		return data, err
	}
	event.Message.Id = id
	return patchedData, nil
}

type thinkingSignatureStreamBlock struct {
	contentType  string
	hasSignature bool
}

type thinkingSignatureStreamState struct {
	responseID string
	blocks     map[int]thinkingSignatureStreamBlock
}

// missingSignatureDelta waits until the block ends so an upstream signature,
// including one delivered in several deltas, always takes precedence.
func (s *thinkingSignatureStreamState) missingSignatureDelta(event *dto.ClaudeResponse) *dto.ClaudeResponse {
	if event == nil {
		return nil
	}

	switch event.Type {
	case "message_start":
		s.responseID = ""
		s.blocks = nil
		if event.Message != nil {
			s.responseID = event.Message.Id
		}
	case "content_block_start":
		index := event.GetIndex()
		if event.ContentBlock == nil {
			delete(s.blocks, index)
			return nil
		}
		if s.blocks == nil {
			s.blocks = make(map[int]thinkingSignatureStreamBlock)
		}
		s.blocks[index] = thinkingSignatureStreamBlock{
			contentType:  event.ContentBlock.Type,
			hasSignature: event.ContentBlock.Signature != "",
		}
	case "content_block_delta":
		if event.Delta == nil {
			return nil
		}
		if event.Delta.Type != "thinking_delta" && event.Delta.Type != "signature_delta" {
			return nil
		}
		if s.blocks == nil {
			s.blocks = make(map[int]thinkingSignatureStreamBlock)
		}
		index := event.GetIndex()
		block := s.blocks[index]
		if block.contentType == "" {
			block.contentType = "thinking"
		}
		block.hasSignature = block.hasSignature || event.Delta.Signature != ""
		s.blocks[index] = block
	case "content_block_stop":
		index := event.GetIndex()
		block, ok := s.blocks[index]
		delete(s.blocks, index)
		if !ok || block.contentType != "thinking" || block.hasSignature || s.responseID == "" {
			return nil
		}
		return &dto.ClaudeResponse{
			Type:  "content_block_delta",
			Index: common.GetPointer(index),
			Delta: &dto.ClaudeMediaMessage{
				Type:      "signature_delta",
				Signature: s.responseID,
			},
		}
	case "message_stop":
		s.responseID = ""
		s.blocks = nil
	}
	return nil
}

func (s *thinkingSignatureStreamState) writeMissingSignature(c *gin.Context, event *dto.ClaudeResponse) error {
	delta := s.missingSignatureDelta(event)
	if delta == nil {
		return nil
	}
	data, err := common.Marshal(delta)
	if err != nil {
		return err
	}
	helper.ClaudeChunkData(c, *delta, string(data))
	return nil
}
