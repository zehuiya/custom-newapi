package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func forceStreamInfo(setting dto.ChannelSettings, mode int) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayMode: mode,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelSetting: setting,
		},
	}
}

func TestApplyForceStreamSupportedRequests(t *testing.T) {
	originalPassThrough := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	t.Cleanup(func() {
		model_setting.GetGlobalSettings().PassThroughRequestEnabled = originalPassThrough
	})

	chatRequest := &dto.GeneralOpenAIRequest{Stream: common.GetPointer(false)}
	chatInfo := forceStreamInfo(dto.ChannelSettings{ForceStream: true}, relayconstant.RelayModeChatCompletions)
	ApplyForceStream(chatInfo, chatRequest)
	require.True(t, *chatRequest.Stream)
	require.True(t, chatInfo.ForceStreamUpstream)
	require.True(t, chatInfo.ForceStreamBuffer)
	require.False(t, chatInfo.IsStream)

	claudeRequest := &dto.ClaudeRequest{Stream: common.GetPointer(false)}
	claudeInfo := forceStreamInfo(dto.ChannelSettings{ForceStream: true}, relayconstant.RelayModeUnknown)
	ApplyForceStream(claudeInfo, claudeRequest)
	require.True(t, *claudeRequest.Stream)
	require.True(t, claudeInfo.ForceStreamUpstream)
	require.True(t, claudeInfo.ForceStreamBuffer)

	responsesRequest := &dto.OpenAIResponsesRequest{Stream: common.GetPointer(false)}
	responsesInfo := forceStreamInfo(dto.ChannelSettings{ForceStream: true}, relayconstant.RelayModeResponses)
	ApplyForceStream(responsesInfo, responsesRequest)
	require.True(t, *responsesRequest.Stream)
	require.True(t, responsesInfo.ForceStreamUpstream)
	require.True(t, responsesInfo.ForceStreamBuffer)
}

func TestApplyForceStreamPreservesExistingBehavior(t *testing.T) {
	originalPassThrough := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	t.Cleanup(func() {
		model_setting.GetGlobalSettings().PassThroughRequestEnabled = originalPassThrough
	})

	t.Run("disabled by default", func(t *testing.T) {
		request := &dto.GeneralOpenAIRequest{Stream: common.GetPointer(false)}
		info := forceStreamInfo(dto.ChannelSettings{}, relayconstant.RelayModeChatCompletions)
		ApplyForceStream(info, request)
		require.False(t, *request.Stream)
		require.False(t, info.ForceStreamUpstream)
		require.False(t, info.ForceStreamBuffer)
	})

	t.Run("client already streams", func(t *testing.T) {
		request := &dto.GeneralOpenAIRequest{Stream: common.GetPointer(true)}
		info := forceStreamInfo(dto.ChannelSettings{ForceStream: true}, relayconstant.RelayModeChatCompletions)
		ApplyForceStream(info, request)
		require.True(t, *request.Stream)
		require.True(t, info.ForceStreamUpstream)
		require.False(t, info.ForceStreamBuffer)
	})

	t.Run("channel pass through", func(t *testing.T) {
		request := &dto.GeneralOpenAIRequest{Stream: common.GetPointer(false)}
		info := forceStreamInfo(dto.ChannelSettings{ForceStream: true, PassThroughBodyEnabled: true}, relayconstant.RelayModeChatCompletions)
		ApplyForceStream(info, request)
		require.False(t, *request.Stream)
		require.False(t, info.ForceStreamUpstream)
		require.False(t, info.ForceStreamBuffer)
	})

	t.Run("unrelated relay mode", func(t *testing.T) {
		request := &dto.GeneralOpenAIRequest{Stream: common.GetPointer(false)}
		info := forceStreamInfo(dto.ChannelSettings{ForceStream: true}, relayconstant.RelayModeEmbeddings)
		ApplyForceStream(info, request)
		require.False(t, *request.Stream)
		require.False(t, info.ForceStreamUpstream)
		require.False(t, info.ForceStreamBuffer)
	})
}

func TestApplyForceStreamSupportsClaudeChannelTestRequest(t *testing.T) {
	originalPassThrough := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	t.Cleanup(func() {
		model_setting.GetGlobalSettings().PassThroughRequestEnabled = originalPassThrough
	})

	request := &dto.GeneralOpenAIRequest{Stream: common.GetPointer(false)}
	info := forceStreamInfo(dto.ChannelSettings{ForceStream: true}, relayconstant.RelayModeUnknown)
	info.IsChannelTest = true
	info.RelayFormat = types.RelayFormatClaude

	ApplyForceStream(info, request)

	require.True(t, *request.Stream)
	require.True(t, info.ForceStreamUpstream)
	require.True(t, info.ForceStreamBuffer)
}

func TestApplyForceStreamBodyWinsOverParamOverride(t *testing.T) {
	info := forceStreamInfo(dto.ChannelSettings{ForceStream: true}, relayconstant.RelayModeChatCompletions)
	info.ForceStreamUpstream = true

	body, err := ApplyForceStreamBody(info, []byte(`{"model":"test","stream":false}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"test","stream":true}`, string(body))
}

func TestForceStreamBufferIsClearedBetweenRetries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "test-model")
	common.SetContextKey(ctx, constant.ContextKeyChannelSetting, dto.ChannelSettings{ForceStream: true})

	request := &dto.GeneralOpenAIRequest{Model: "test-model", Stream: common.GetPointer(false)}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeChatCompletions,
		OriginModelName: "test-model",
		Request:         request,
	}
	info.InitChannelMeta(ctx)
	ApplyForceStream(info, request)
	require.True(t, info.ForceStreamUpstream)
	require.True(t, info.ForceStreamBuffer)

	common.SetContextKey(ctx, constant.ContextKeyChannelSetting, dto.ChannelSettings{})
	info.InitChannelMeta(ctx)
	require.False(t, info.ForceStreamUpstream)
	require.False(t, info.ForceStreamBuffer)
}
