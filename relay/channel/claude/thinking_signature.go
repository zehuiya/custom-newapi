package claude

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/tidwall/sjson"
)

func shouldFillClaudeThinkingSignature(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ChannelMeta != nil && info.ChannelType == constant.ChannelTypeAnthropic
}

func newClaudeResponseID() string {
	return "msg_" + common.GetUUID()
}

// patchClaudeThinkingSignatures supplies a compatibility placeholder, not an
// Anthropic cryptographic signature. Patch the original JSON to retain provider
// fields and preserve existing signatures exactly.
func patchClaudeThinkingSignatures(data []byte, response *dto.ClaudeResponse) ([]byte, error) {
	if response.Id == "" {
		response.Id = newClaudeResponseID()
		var err error
		data, err = sjson.SetBytes(data, "id", response.Id)
		if err != nil {
			return nil, err
		}
	}
	for index, block := range response.Content {
		if block.Type != "thinking" || block.Signature != "" {
			continue
		}
		var err error
		data, err = sjson.SetBytes(data, "content."+strconv.Itoa(index)+".signature", response.Id)
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}
