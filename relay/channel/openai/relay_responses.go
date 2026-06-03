package openai

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if responsesResponse.ID != "" {
		info.UpstreamResponseId = responsesResponse.ID
	}

	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	if responsesResponse.HasImageGenerationCall() {
		c.Set("image_generation_call", true)
		c.Set("image_generation_call_quality", responsesResponse.GetQuality())
		c.Set("image_generation_call_size", responsesResponse.GetSize())
	}

	// compute usage
	usage := dto.Usage{}
	if responsesResponse.Usage != nil {
		fillRelayUsageFromResponsesUsage(&usage, responsesResponse.Usage)
		if service.NormalizeNoCacheUsageForRelay(c, info, &usage) {
			syncResponsesUsageFieldsFromRelayUsage(responsesResponse.Usage, &usage)
			if modifiedBody, changed := rewriteResponsesUsagePayload(responseBody, false, &usage); changed {
				responseBody = modifiedBody
			}
		}
	}

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	if info == nil || info.ResponsesUsageInfo == nil || info.ResponsesUsageInfo.BuiltInTools == nil {
		markOpenAIUsageSemantic(&usage)
		return &usage, nil
	}
	// 解析 Tools 用量
	for _, tool := range responsesResponse.Tools {
		buildToolinfo, ok := info.ResponsesUsageInfo.BuiltInTools[common.Interface2String(tool["type"])]
		if !ok || buildToolinfo == nil {
			logger.LogError(c, fmt.Sprintf("BuiltInTools not found for tool type: %v", tool["type"]))
			continue
		}
		buildToolinfo.CallCount++
	}
	markOpenAIUsageSemantic(&usage)
	return &usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	var usage = &dto.Usage{}
	var responseTextBuilder strings.Builder

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {

		// 检查当前数据是否包含 completed 状态和 usage 信息
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
			sr.Error(err)
			return
		}
		switch streamResponse.Type {
		case "response.completed":
			if streamResponse.Response != nil {
				if streamResponse.Response.ID != "" {
					info.UpstreamResponseId = streamResponse.Response.ID
				}
				if streamResponse.Response.Usage != nil {
					fillRelayUsageFromResponsesUsage(usage, streamResponse.Response.Usage)
					if service.NormalizeNoCacheUsageForRelay(c, info, usage) {
						syncResponsesUsageFieldsFromRelayUsage(streamResponse.Response.Usage, usage)
						if modifiedData, changed := rewriteResponsesUsagePayload(common.StringToByteSlice(data), true, usage); changed {
							data = string(modifiedData)
						}
					}
				}
				if streamResponse.Response.HasImageGenerationCall() {
					c.Set("image_generation_call", true)
					c.Set("image_generation_call_quality", streamResponse.Response.GetQuality())
					c.Set("image_generation_call_size", streamResponse.Response.GetSize())
				}
			}
		}

		sendResponsesStreamData(c, streamResponse, data)

		switch streamResponse.Type {
		case "response.output_text.delta":
			responseTextBuilder.WriteString(streamResponse.Delta)
		case dto.ResponsesOutputTypeItemDone:
			if streamResponse.Item != nil {
				switch streamResponse.Item.Type {
				case dto.BuildInCallWebSearchCall:
					if info != nil && info.ResponsesUsageInfo != nil && info.ResponsesUsageInfo.BuiltInTools != nil {
						if webSearchTool, exists := info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview]; exists && webSearchTool != nil {
							webSearchTool.CallCount++
						}
					}
				}
			}
		}
	})

	if usage.CompletionTokens == 0 {
		// 计算输出文本的 token 数量
		tempStr := responseTextBuilder.String()
		if len(tempStr) > 0 {
			// 非正常结束，使用输出文本的 token 数量
			completionTokens := service.CountTextToken(tempStr, info.UpstreamModelName)
			usage.CompletionTokens = completionTokens
		}
	}

	if usage.PromptTokens == 0 && usage.CompletionTokens != 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}

	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	markOpenAIUsageSemantic(usage)
	return usage, nil
}

func fillRelayUsageFromResponsesUsage(usage *dto.Usage, responsesUsage *dto.Usage) {
	if usage == nil || responsesUsage == nil {
		return
	}
	if responsesUsage.InputTokens != 0 {
		usage.PromptTokens = responsesUsage.InputTokens
		usage.InputTokens = responsesUsage.InputTokens
	}
	if responsesUsage.OutputTokens != 0 {
		usage.CompletionTokens = responsesUsage.OutputTokens
		usage.OutputTokens = responsesUsage.OutputTokens
	}
	if responsesUsage.TotalTokens != 0 {
		usage.TotalTokens = responsesUsage.TotalTokens
	} else {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	if responsesUsage.InputTokensDetails != nil {
		inputDetails := *responsesUsage.InputTokensDetails
		usage.InputTokensDetails = &inputDetails
		usage.PromptTokensDetails.CachedTokens = inputDetails.CachedTokens
		usage.PromptTokensDetails.ImageTokens = inputDetails.ImageTokens
		usage.PromptTokensDetails.AudioTokens = inputDetails.AudioTokens
	}
	if responsesUsage.CompletionTokenDetails.ReasoningTokens != 0 {
		usage.CompletionTokenDetails.ReasoningTokens = responsesUsage.CompletionTokenDetails.ReasoningTokens
	}
}

func syncResponsesUsageFieldsFromRelayUsage(responsesUsage *dto.Usage, usage *dto.Usage) {
	if responsesUsage == nil || usage == nil {
		return
	}
	responsesUsage.InputTokens = usage.PromptTokens
	responsesUsage.OutputTokens = usage.CompletionTokens
	responsesUsage.TotalTokens = usage.TotalTokens
	if responsesUsage.InputTokensDetails == nil {
		responsesUsage.InputTokensDetails = &dto.InputTokenDetails{}
	}
	responsesUsage.InputTokensDetails.CachedTokens = usage.PromptTokensDetails.CachedTokens
	responsesUsage.InputTokensDetails.ImageTokens = usage.PromptTokensDetails.ImageTokens
	responsesUsage.InputTokensDetails.AudioTokens = usage.PromptTokensDetails.AudioTokens
}

func rewriteResponsesUsagePayload(data []byte, nestedResponse bool, usage *dto.Usage) ([]byte, bool) {
	if len(data) == 0 || usage == nil {
		return data, false
	}

	var payload map[string]interface{}
	if err := common.Unmarshal(data, &payload); err != nil {
		return data, false
	}

	usageContainer := payload
	if nestedResponse {
		responseMap, ok := payload["response"].(map[string]interface{})
		if !ok {
			return data, false
		}
		usageContainer = responseMap
	}

	usageMap, ok := usageContainer["usage"].(map[string]interface{})
	if !ok {
		return data, false
	}
	usageMap["input_tokens"] = usage.PromptTokens
	usageMap["output_tokens"] = usage.CompletionTokens
	usageMap["total_tokens"] = usage.TotalTokens

	inputDetails, ok := usageMap["input_tokens_details"].(map[string]interface{})
	if ok {
		inputDetails["cached_tokens"] = usage.PromptTokensDetails.CachedTokens
	} else {
		usageMap["input_tokens_details"] = map[string]interface{}{
			"cached_tokens": usage.PromptTokensDetails.CachedTokens,
		}
	}

	modifiedData, err := common.Marshal(payload)
	if err != nil {
		return data, false
	}
	return modifiedData, true
}
