package relay

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/samber/lo"
	"github.com/tidwall/sjson"
)

// ApplyForceStream makes only the upstream request streaming. RelayInfo.IsStream
// remains the client's requested response mode, while ForceStreamBuffer tells
// the response adaptor to aggregate upstream SSE back into one JSON response.
func ApplyForceStream(info *relaycommon.RelayInfo, request dto.Request) {
	if info == nil {
		return
	}

	info.ForceStreamUpstream = false
	info.ForceStreamBuffer = false
	if !info.ChannelSetting.ForceStream ||
		info.ChannelSetting.PassThroughBodyEnabled ||
		model_setting.GetGlobalSettings().PassThroughRequestEnabled {
		return
	}

	switch req := request.(type) {
	case *dto.GeneralOpenAIRequest:
		isClaudeChannelTest := info.IsChannelTest &&
			info.RelayMode == relayconstant.RelayModeUnknown &&
			info.RelayFormat == types.RelayFormatClaude
		if info.RelayMode != relayconstant.RelayModeChatCompletions && !isClaudeChannelTest {
			return
		}
		info.ForceStreamBuffer = !lo.FromPtrOr(req.Stream, false)
		req.Stream = common.GetPointer(true)
	case *dto.ClaudeRequest:
		// Native /v1/messages requests intentionally keep RelayModeUnknown in
		// the existing routing model; the concrete request type identifies the
		// supported Claude Messages path here.
		if info.RelayMode != relayconstant.RelayModeUnknown && info.RelayMode != relayconstant.RelayModeChatCompletions {
			return
		}
		info.ForceStreamBuffer = !lo.FromPtrOr(req.Stream, false)
		req.Stream = common.GetPointer(true)
	case *dto.OpenAIResponsesRequest:
		if info.RelayMode != relayconstant.RelayModeResponses {
			return
		}
		info.ForceStreamBuffer = !lo.FromPtrOr(req.Stream, false)
		req.Stream = common.GetPointer(true)
	default:
		return
	}

	info.ForceStreamUpstream = true
}

// ApplyForceStreamBody reapplies stream:true after channel parameter
// overrides, so a lower-priority override cannot undo the channel setting.
func ApplyForceStreamBody(info *relaycommon.RelayInfo, body []byte) ([]byte, error) {
	if info == nil || !info.ForceStreamUpstream {
		return body, nil
	}
	return sjson.SetBytes(body, "stream", true)
}
