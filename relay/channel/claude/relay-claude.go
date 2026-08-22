package claude

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel/openrouter"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relay/reasonmap"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	WebSearchMaxUsesLow    = 1
	WebSearchMaxUsesMedium = 5
	WebSearchMaxUsesHigh   = 10
)

func stopReasonClaude2OpenAI(reason string) string {
	return reasonmap.ClaudeStopReasonToOpenAIFinishReason(reason)
}

func maybeMarkClaudeRefusal(c *gin.Context, stopReason string) {
	if c == nil {
		return
	}
	if strings.EqualFold(stopReason, "refusal") {
		common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "claude_stop_reason=refusal")
	}
}

func RequestOpenAI2ClaudeMessage(c *gin.Context, textRequest dto.GeneralOpenAIRequest) (*dto.ClaudeRequest, error) {
	claudeTools := make([]any, 0, len(textRequest.Tools))

	for _, tool := range textRequest.Tools {
		if params, ok := tool.Function.Parameters.(map[string]any); ok {
			claudeTool := dto.Tool{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
			}
			claudeTool.InputSchema = make(map[string]interface{})
			if params["type"] != nil {
				claudeTool.InputSchema["type"] = params["type"].(string)
			}
			claudeTool.InputSchema["properties"] = params["properties"]
			claudeTool.InputSchema["required"] = params["required"]
			for s, a := range params {
				if s == "type" || s == "properties" || s == "required" {
					continue
				}
				claudeTool.InputSchema[s] = a
			}
			claudeTools = append(claudeTools, &claudeTool)
		}
	}

	// Web search tool
	// https://docs.anthropic.com/en/docs/agents-and-tools/tool-use/web-search-tool
	if textRequest.WebSearchOptions != nil {
		webSearchTool := dto.ClaudeWebSearchTool{
			Type: "web_search_20250305",
			Name: "web_search",
		}

		// 处理 user_location
		if textRequest.WebSearchOptions.UserLocation != nil {
			anthropicUserLocation := &dto.ClaudeWebSearchUserLocation{
				Type: "approximate", // 固定为 "approximate"
			}

			// 解析 UserLocation JSON
			var userLocationMap map[string]interface{}
			if err := common.Unmarshal(textRequest.WebSearchOptions.UserLocation, &userLocationMap); err == nil {
				// 检查是否有 approximate 字段
				if approximateData, ok := userLocationMap["approximate"].(map[string]interface{}); ok {
					if timezone, ok := approximateData["timezone"].(string); ok && timezone != "" {
						anthropicUserLocation.Timezone = timezone
					}
					if country, ok := approximateData["country"].(string); ok && country != "" {
						anthropicUserLocation.Country = country
					}
					if region, ok := approximateData["region"].(string); ok && region != "" {
						anthropicUserLocation.Region = region
					}
					if city, ok := approximateData["city"].(string); ok && city != "" {
						anthropicUserLocation.City = city
					}
				}
			}

			webSearchTool.UserLocation = anthropicUserLocation
		}

		// 处理 search_context_size 转换为 max_uses
		if textRequest.WebSearchOptions.SearchContextSize != "" {
			switch textRequest.WebSearchOptions.SearchContextSize {
			case "low":
				webSearchTool.MaxUses = WebSearchMaxUsesLow
			case "medium":
				webSearchTool.MaxUses = WebSearchMaxUsesMedium
			case "high":
				webSearchTool.MaxUses = WebSearchMaxUsesHigh
			}
		}

		claudeTools = append(claudeTools, &webSearchTool)
	}

	claudeRequest := dto.ClaudeRequest{
		Model:         textRequest.Model,
		StopSequences: nil,
		Temperature:   textRequest.Temperature,
		Tools:         claudeTools,
	}
	if maxTokens := textRequest.GetMaxTokens(); maxTokens > 0 {
		claudeRequest.MaxTokens = common.GetPointer(maxTokens)
	}
	if textRequest.TopP != nil {
		claudeRequest.TopP = common.GetPointer(*textRequest.TopP)
	}
	if textRequest.TopK != nil {
		claudeRequest.TopK = common.GetPointer(*textRequest.TopK)
	}
	if textRequest.IsStream(nil) {
		claudeRequest.Stream = common.GetPointer(true)
	}

	// 处理 tool_choice 和 parallel_tool_calls
	if textRequest.ToolChoice != nil || textRequest.ParallelTooCalls != nil {
		claudeToolChoice := mapToolChoice(textRequest.ToolChoice, textRequest.ParallelTooCalls)
		if claudeToolChoice != nil {
			claudeRequest.ToolChoice = claudeToolChoice
		}
	}

	if claudeRequest.MaxTokens == nil || *claudeRequest.MaxTokens == 0 {
		defaultMaxTokens := uint(model_setting.GetClaudeSettings().GetDefaultMaxTokens(textRequest.Model))
		claudeRequest.MaxTokens = &defaultMaxTokens
	}

	if baseModel, effortLevel, ok := reasoning.TrimEffortSuffix(textRequest.Model); ok && effortLevel != "" &&
		(strings.HasPrefix(textRequest.Model, "claude-opus-4-6") || strings.HasPrefix(textRequest.Model, "claude-opus-4-7")) {
		claudeRequest.Model = baseModel
		claudeRequest.Thinking = &dto.Thinking{
			Type: "adaptive",
		}
		claudeRequest.OutputConfig = json.RawMessage(fmt.Sprintf(`{"effort":"%s"}`, effortLevel))
		if strings.HasPrefix(baseModel, "claude-opus-4-7") {
			// Opus 4.7 rejects non-default temperature/top_p/top_k with 400
			// and defaults display to "omitted"; restore the 4.6 visible summary.
			claudeRequest.Thinking.Display = "summarized"
			claudeRequest.Temperature = nil
			claudeRequest.TopP = nil
			claudeRequest.TopK = nil
		} else {
			claudeRequest.TopP = nil
			claudeRequest.Temperature = common.GetPointer[float64](1.0)
		}
	} else if model_setting.GetClaudeSettings().ThinkingAdapterEnabled &&
		strings.HasSuffix(textRequest.Model, "-thinking") {

		trimmedModel := strings.TrimSuffix(textRequest.Model, "-thinking")
		if strings.HasPrefix(trimmedModel, "claude-opus-4-7") {
			// Opus 4.7 rejects thinking.type="enabled"; use adaptive at high effort.
			claudeRequest.Thinking = &dto.Thinking{Type: "adaptive", Display: "summarized"}
			claudeRequest.OutputConfig = json.RawMessage(`{"effort":"high"}`)
			claudeRequest.Temperature = nil
			claudeRequest.TopP = nil
			claudeRequest.TopK = nil
		} else {
			// 因为BudgetTokens 必须大于1024
			if claudeRequest.MaxTokens == nil || *claudeRequest.MaxTokens < 1280 {
				claudeRequest.MaxTokens = common.GetPointer[uint](1280)
			}

			// BudgetTokens 为 max_tokens 的 80%
			claudeRequest.Thinking = &dto.Thinking{
				Type:         "enabled",
				BudgetTokens: common.GetPointer[int](int(float64(*claudeRequest.MaxTokens) * model_setting.GetClaudeSettings().ThinkingAdapterBudgetTokensPercentage)),
			}
			// TODO: 临时处理
			// https://docs.anthropic.com/en/docs/build-with-claude/extended-thinking#important-considerations-when-using-extended-thinking
			claudeRequest.TopP = nil
			claudeRequest.Temperature = common.GetPointer[float64](1.0)
		}
		if !model_setting.ShouldPreserveThinkingSuffix(textRequest.Model) {
			claudeRequest.Model = trimmedModel
		}
	}

	if textRequest.ReasoningEffort != "" {
		switch textRequest.ReasoningEffort {
		case "low":
			claudeRequest.Thinking = &dto.Thinking{
				Type:         "enabled",
				BudgetTokens: common.GetPointer[int](1280),
			}
		case "medium":
			claudeRequest.Thinking = &dto.Thinking{
				Type:         "enabled",
				BudgetTokens: common.GetPointer[int](2048),
			}
		case "high":
			claudeRequest.Thinking = &dto.Thinking{
				Type:         "enabled",
				BudgetTokens: common.GetPointer[int](4096),
			}
		}
	}

	// 指定了 reasoning 参数,覆盖 budgetTokens
	if textRequest.Reasoning != nil {
		var reasoning openrouter.RequestReasoning
		if err := common.Unmarshal(textRequest.Reasoning, &reasoning); err != nil {
			return nil, err
		}

		budgetTokens := reasoning.MaxTokens
		if budgetTokens > 0 {
			claudeRequest.Thinking = &dto.Thinking{
				Type:         "enabled",
				BudgetTokens: &budgetTokens,
			}
		}
	}

	if textRequest.Stop != nil {
		// stop maybe string/array string, convert to array string
		switch textRequest.Stop.(type) {
		case string:
			claudeRequest.StopSequences = []string{textRequest.Stop.(string)}
		case []interface{}:
			stopSequences := make([]string, 0)
			for _, stop := range textRequest.Stop.([]interface{}) {
				stopSequences = append(stopSequences, stop.(string))
			}
			claudeRequest.StopSequences = stopSequences
		}
	}
	formatMessages := make([]dto.Message, 0)
	lastMessage := dto.Message{
		Role: "tool",
	}
	for i, message := range textRequest.Messages {
		if message.Role == "" {
			textRequest.Messages[i].Role = "user"
		}
		fmtMessage := dto.Message{
			Role:    message.Role,
			Content: message.Content,
		}
		if message.Role == "tool" {
			fmtMessage.ToolCallId = message.ToolCallId
		}
		if message.Role == "assistant" && message.ToolCalls != nil {
			fmtMessage.ToolCalls = message.ToolCalls
		}
		if lastMessage.Role == message.Role && lastMessage.Role != "tool" {
			if lastMessage.IsStringContent() && message.IsStringContent() {
				fmtMessage.SetStringContent(strings.Trim(fmt.Sprintf("%s %s", lastMessage.StringContent(), message.StringContent()), "\""))
				// delete last message
				formatMessages = formatMessages[:len(formatMessages)-1]
			}
		}
		if fmtMessage.Content == nil || (fmtMessage.IsStringContent() && fmtMessage.StringContent() == "") {
			fmtMessage.SetStringContent("...")
		}
		formatMessages = append(formatMessages, fmtMessage)
		lastMessage = fmtMessage
	}

	claudeMessages := make([]dto.ClaudeMessage, 0)
	isFirstMessage := true
	// 初始化system消息数组，用于累积多个system消息
	var systemMessages []dto.ClaudeMediaMessage

	for _, message := range formatMessages {
		if message.Role == "system" {
			// 根据Claude API规范，system字段使用数组格式更有通用性
			if message.IsStringContent() {
				if text := message.StringContent(); text != "" {
					systemMessages = append(systemMessages, dto.ClaudeMediaMessage{
						Type: "text",
						Text: common.GetPointer[string](text),
					})
				}
			} else {
				// 支持复合内容的system消息（虽然不常见，但需要考虑完整性）
				for _, ctx := range message.ParseContent() {
					if ctx.Type == "text" && ctx.Text != "" {
						systemMessages = append(systemMessages, dto.ClaudeMediaMessage{
							Type: "text",
							Text: common.GetPointer[string](ctx.Text),
						})
					}
					// 未来可以在这里扩展对图片等其他类型的支持
				}
			}
		} else {
			if isFirstMessage {
				isFirstMessage = false
				if message.Role != "user" {
					// fix: first message is assistant, add user message
					claudeMessage := dto.ClaudeMessage{
						Role: "user",
						Content: []dto.ClaudeMediaMessage{
							{
								Type: "text",
								Text: common.GetPointer[string]("..."),
							},
						},
					}
					claudeMessages = append(claudeMessages, claudeMessage)
				}
			}
			claudeMessage := dto.ClaudeMessage{
				Role: message.Role,
			}
			if message.Role == "tool" {
				if len(claudeMessages) > 0 && claudeMessages[len(claudeMessages)-1].Role == "user" {
					lastMessage := claudeMessages[len(claudeMessages)-1]
					if content, ok := lastMessage.Content.(string); ok {
						lastMessage.Content = []dto.ClaudeMediaMessage{
							{
								Type: "text",
								Text: common.GetPointer[string](content),
							},
						}
					}
					lastMessage.Content = append(lastMessage.Content.([]dto.ClaudeMediaMessage), dto.ClaudeMediaMessage{
						Type:      "tool_result",
						ToolUseId: message.ToolCallId,
						Content:   message.Content,
					})
					claudeMessages[len(claudeMessages)-1] = lastMessage
					continue
				} else {
					claudeMessage.Role = "user"
					claudeMessage.Content = []dto.ClaudeMediaMessage{
						{
							Type:      "tool_result",
							ToolUseId: message.ToolCallId,
							Content:   message.Content,
						},
					}
				}
			} else if message.IsStringContent() && message.ToolCalls == nil {
				text := message.StringContent()
				if text == "" {
					text = "..."
				}
				claudeMessage.Content = text
			} else {
				claudeMediaMessages := make([]dto.ClaudeMediaMessage, 0)
				for _, mediaMessage := range message.ParseContent() {
					switch mediaMessage.Type {
					case "text":
						if mediaMessage.Text != "" {
							claudeMediaMessages = append(claudeMediaMessages, dto.ClaudeMediaMessage{
								Type: "text",
								Text: common.GetPointer[string](mediaMessage.Text),
							})
						}
					default:
						source := mediaMessage.ToFileSource()
						if source == nil {
							continue
						}
						base64Data, mimeType, err := service.GetBase64Data(c, source, "formatting image for Claude")
						if err != nil {
							return nil, fmt.Errorf("get file data failed: %s", err.Error())
						}
						claudeMediaMessage := dto.ClaudeMediaMessage{
							Source: &dto.ClaudeMessageSource{
								Type: "base64",
							},
						}
						if strings.HasPrefix(mimeType, "application/pdf") {
							claudeMediaMessage.Type = "document"
						} else {
							claudeMediaMessage.Type = "image"
						}

						claudeMediaMessage.Source.MediaType = mimeType
						claudeMediaMessage.Source.Data = base64Data
						claudeMediaMessages = append(claudeMediaMessages, claudeMediaMessage)
						continue
					}
				}

				if message.ToolCalls != nil {
					for _, toolCall := range message.ParseToolCalls() {
						inputObj := make(map[string]any)
						if err := common.Unmarshal([]byte(toolCall.Function.Arguments), &inputObj); err != nil {
							common.SysLog("tool call function arguments is not a map[string]any: " + fmt.Sprintf("%v", toolCall.Function.Arguments))
							continue
						}
						claudeMediaMessages = append(claudeMediaMessages, dto.ClaudeMediaMessage{
							Type:  "tool_use",
							Id:    toolCall.ID,
							Name:  toolCall.Function.Name,
							Input: inputObj,
						})
					}
				}
				claudeMessage.Content = claudeMediaMessages
			}
			claudeMessages = append(claudeMessages, claudeMessage)
		}
	}

	// 设置累积的system消息
	if len(systemMessages) > 0 {
		claudeRequest.System = systemMessages
	}

	claudeRequest.Prompt = ""
	claudeRequest.Messages = claudeMessages
	return &claudeRequest, nil
}

func StreamResponseClaude2OpenAI(claudeResponse *dto.ClaudeResponse) *dto.ChatCompletionsStreamResponse {
	var response dto.ChatCompletionsStreamResponse
	response.Object = "chat.completion.chunk"
	response.Model = claudeResponse.Model
	response.Choices = make([]dto.ChatCompletionsStreamResponseChoice, 0)
	tools := make([]dto.ToolCallResponse, 0)
	fcIdx := 0
	if claudeResponse.Index != nil {
		fcIdx = *claudeResponse.Index - 1
		if fcIdx < 0 {
			fcIdx = 0
		}
	}
	var choice dto.ChatCompletionsStreamResponseChoice
	if claudeResponse.Type == "message_start" {
		if claudeResponse.Message != nil {
			response.Id = claudeResponse.Message.Id
			response.Model = claudeResponse.Message.Model
		}
		//claudeUsage = &claudeResponse.Message.Usage
		choice.Delta.SetContentString("")
		choice.Delta.Role = "assistant"
	} else if claudeResponse.Type == "content_block_start" {
		if claudeResponse.ContentBlock != nil {
			// 如果是文本块，尽可能发送首段文本（若存在）
			if claudeResponse.ContentBlock.Type == "text" && claudeResponse.ContentBlock.Text != nil {
				choice.Delta.SetContentString(*claudeResponse.ContentBlock.Text)
			}
			if claudeResponse.ContentBlock.Type == "tool_use" {
				tools = append(tools, dto.ToolCallResponse{
					Index: common.GetPointer(fcIdx),
					ID:    claudeResponse.ContentBlock.Id,
					Type:  "function",
					Function: dto.FunctionResponse{
						Name:      claudeResponse.ContentBlock.Name,
						Arguments: "",
					},
				})
			}
		} else {
			return nil
		}
	} else if claudeResponse.Type == "content_block_delta" {
		if claudeResponse.Delta != nil {
			choice.Delta.Content = claudeResponse.Delta.Text
			switch claudeResponse.Delta.Type {
			case "input_json_delta":
				tools = append(tools, dto.ToolCallResponse{
					Type:  "function",
					Index: common.GetPointer(fcIdx),
					Function: dto.FunctionResponse{
						Arguments: *claudeResponse.Delta.PartialJson,
					},
				})
			case "signature_delta":
				// 加密的不处理
				signatureContent := "\n"
				choice.Delta.ReasoningContent = &signatureContent
			case "thinking_delta":
				choice.Delta.ReasoningContent = claudeResponse.Delta.Thinking
			}
		}
	} else if claudeResponse.Type == "message_delta" {
		if claudeResponse.Delta != nil && claudeResponse.Delta.StopReason != nil {
			finishReason := stopReasonClaude2OpenAI(*claudeResponse.Delta.StopReason)
			if finishReason != "null" {
				choice.FinishReason = &finishReason
			}
		}
		//claudeUsage = &claudeResponse.Usage
	} else if claudeResponse.Type == "message_stop" {
		return nil
	} else {
		return nil
	}
	if len(tools) > 0 {
		choice.Delta.Content = nil // compatible with other OpenAI derivative applications, like LobeOpenAICompatibleFactory ...
		choice.Delta.ToolCalls = tools
	}
	response.Choices = append(response.Choices, choice)

	return &response
}

func ResponseClaude2OpenAI(claudeResponse *dto.ClaudeResponse) *dto.OpenAITextResponse {
	choices := make([]dto.OpenAITextResponseChoice, 0)
	fullTextResponse := dto.OpenAITextResponse{
		Id:      fmt.Sprintf("chatcmpl-%s", common.GetUUID()),
		Object:  "chat.completion",
		Created: common.GetTimestamp(),
	}
	var responseText string
	var responseThinking string
	if len(claudeResponse.Content) > 0 {
		responseText = claudeResponse.Content[0].GetText()
		if claudeResponse.Content[0].Thinking != nil {
			responseThinking = *claudeResponse.Content[0].Thinking
		}
	}
	tools := make([]dto.ToolCallResponse, 0)
	thinkingContent := ""

	fullTextResponse.Id = claudeResponse.Id
	for _, message := range claudeResponse.Content {
		switch message.Type {
		case "tool_use":
			args, _ := common.Marshal(message.Input)
			tools = append(tools, dto.ToolCallResponse{
				ID:   message.Id,
				Type: "function", // compatible with other OpenAI derivative applications
				Function: dto.FunctionResponse{
					Name:      message.Name,
					Arguments: string(args),
				},
			})
		case "thinking":
			// 加密的不管， 只输出明文的推理过程
			if message.Thinking != nil {
				thinkingContent = *message.Thinking
			}
		case "text":
			responseText = message.GetText()
		}
	}
	choice := dto.OpenAITextResponseChoice{
		Index: 0,
		Message: dto.Message{
			Role: "assistant",
		},
		FinishReason: stopReasonClaude2OpenAI(claudeResponse.StopReason),
	}
	choice.SetStringContent(responseText)
	if len(responseThinking) > 0 {
		choice.ReasoningContent = responseThinking
	}
	if len(tools) > 0 {
		choice.Message.SetToolCalls(tools)
	}
	choice.Message.ReasoningContent = thinkingContent
	fullTextResponse.Model = claudeResponse.Model
	choices = append(choices, choice)
	fullTextResponse.Choices = choices
	return &fullTextResponse
}

type ClaudeResponseInfo struct {
	ResponseId    string
	Created       int64
	Model         string
	ResponseText  strings.Builder
	ReasoningText strings.Builder
	OutputText    strings.Builder
	Usage         *dto.Usage
	Done          bool
}

func collectClaudeResponseTextAndReasoning(claudeResponse *dto.ClaudeResponse) (string, string, string) {
	if claudeResponse == nil {
		return "", "", ""
	}
	var responseText strings.Builder
	var reasoningText strings.Builder
	var outputText strings.Builder
	for _, content := range claudeResponse.Content {
		switch content.Type {
		case "text":
			text := content.GetText()
			responseText.WriteString(text)
			outputText.WriteString(text)
		case "thinking":
			if content.Thinking != nil {
				thinking := *content.Thinking
				responseText.WriteString(thinking)
				reasoningText.WriteString(thinking)
			}
		case "tool_use":
			outputText.WriteString(content.Name)
			if content.Input != nil {
				args, _ := common.Marshal(content.Input)
				outputText.Write(args)
			}
		}
	}
	return responseText.String(), reasoningText.String(), outputText.String()
}

func cacheCreationTokensForOpenAIUsage(usage *dto.Usage) int {
	if usage == nil {
		return 0
	}
	splitCacheCreationTokens := usage.ClaudeCacheCreation5mTokens + usage.ClaudeCacheCreation1hTokens
	if splitCacheCreationTokens == 0 {
		return usage.PromptTokensDetails.CachedCreationTokens
	}
	if usage.PromptTokensDetails.CachedCreationTokens > splitCacheCreationTokens {
		return usage.PromptTokensDetails.CachedCreationTokens
	}
	return splitCacheCreationTokens
}

const normalizedAnthropicInclusiveCacheUsageSource = "anthropic_inclusive_cache_normalized"

func shouldNormalizeAnthropicInclusiveCacheUsage(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil {
		return false
	}
	if info.ChannelOtherSettings.ClaudeInputTokensIncludesCache {
		return true
	}
	return strings.Contains(strings.ToLower(info.ChannelName), "sub2api")
}

func normalizeAnthropicInclusiveCacheUsage(info *relaycommon.RelayInfo, usage *dto.Usage) bool {
	if usage == nil || usage.UsageSource == normalizedAnthropicInclusiveCacheUsageSource || !shouldNormalizeAnthropicInclusiveCacheUsage(info) {
		return false
	}
	inclusiveCacheTokens := usage.PromptTokensDetails.CachedTokens + cacheCreationTokensForOpenAIUsage(usage)
	if inclusiveCacheTokens <= 0 || usage.PromptTokens < inclusiveCacheTokens {
		return false
	}
	usage.PromptTokens -= inclusiveCacheTokens
	usage.InputTokens = usage.PromptTokens
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	usage.UsageSemantic = "anthropic"
	usage.UsageSource = normalizedAnthropicInclusiveCacheUsageSource
	return true
}

func buildOpenAIStyleUsageFromClaudeUsage(usage *dto.Usage) dto.Usage {
	if usage == nil {
		return dto.Usage{}
	}
	clone := *usage
	clone.ClaudeCacheCreation5mTokens, clone.ClaudeCacheCreation1hTokens = service.NormalizeCacheCreationSplit(
		usage.PromptTokensDetails.CachedCreationTokens,
		usage.ClaudeCacheCreation5mTokens,
		usage.ClaudeCacheCreation1hTokens,
	)
	cacheCreationTokens := cacheCreationTokensForOpenAIUsage(usage)
	totalInputTokens := usage.PromptTokens + usage.PromptTokensDetails.CachedTokens + cacheCreationTokens
	clone.PromptTokens = totalInputTokens
	clone.InputTokens = totalInputTokens
	clone.TotalTokens = totalInputTokens + usage.CompletionTokens
	clone.UsageSemantic = "openai"
	clone.UsageSource = "anthropic"
	return clone
}

func mergeClaudeUsageIntoRelayUsage(relayUsage *dto.Usage, claudeUsage *dto.ClaudeUsage) {
	if relayUsage == nil || claudeUsage == nil {
		return
	}
	relayUsage.UsageSemantic = "anthropic"
	if inputTokens := claudeUsage.GetInputTokens(); inputTokens > 0 {
		relayUsage.PromptTokens = inputTokens
	}
	if outputTokens := claudeUsage.GetOutputTokens(); outputTokens > 0 {
		relayUsage.CompletionTokens = outputTokens
	}
	if cacheReadTokens := claudeUsage.GetCacheReadInputTokens(); cacheReadTokens > 0 {
		relayUsage.PromptTokensDetails.CachedTokens = cacheReadTokens
	}
	if cacheCreationTokens := claudeUsage.GetCacheCreationTotalTokens(); cacheCreationTokens > 0 {
		relayUsage.PromptTokensDetails.CachedCreationTokens = cacheCreationTokens
	}
	if cacheCreation5m := claudeUsage.GetCacheCreation5mTokens(); cacheCreation5m > 0 {
		relayUsage.ClaudeCacheCreation5mTokens = cacheCreation5m
	}
	if cacheCreation1h := claudeUsage.GetCacheCreation1hTokens(); cacheCreation1h > 0 {
		relayUsage.ClaudeCacheCreation1hTokens = cacheCreation1h
	}
	relayUsage.TotalTokens = relayUsage.PromptTokens + relayUsage.CompletionTokens
}

func syncClaudeUsageFieldsFromRelayUsage(claudeUsage *dto.ClaudeUsage, relayUsage *dto.Usage) {
	if claudeUsage == nil || relayUsage == nil {
		return
	}
	claudeUsage.InputTokens = relayUsage.PromptTokens
	claudeUsage.OutputTokens = relayUsage.CompletionTokens
	claudeUsage.CacheReadInputTokens = relayUsage.PromptTokensDetails.CachedTokens
	claudeUsage.CacheCreationInputTokens = relayUsage.PromptTokensDetails.CachedCreationTokens

	cacheCreation5m, cacheCreation1h := service.NormalizeCacheCreationSplit(
		relayUsage.PromptTokensDetails.CachedCreationTokens,
		relayUsage.ClaudeCacheCreation5mTokens,
		relayUsage.ClaudeCacheCreation1hTokens,
	)
	if cacheCreation5m > 0 || cacheCreation1h > 0 {
		if claudeUsage.CacheCreation == nil {
			claudeUsage.CacheCreation = &dto.ClaudeCacheCreationUsage{}
		}
		claudeUsage.CacheCreation.Ephemeral5mInputTokens = cacheCreation5m
		claudeUsage.CacheCreation.Ephemeral1hInputTokens = cacheCreation1h
	}
}

func injectSyntheticCacheInfoForClaudeUsage(usage *dto.Usage, info *relaycommon.RelayInfo) bool {
	if usage == nil || info == nil {
		return false
	}

	existingCachedTokens := usage.PromptTokensDetails.CachedTokens
	totalInputTokens := usage.PromptTokens + existingCachedTokens
	cachedTokens, changed := info.CalculateSyntheticCacheTarget(totalInputTokens, existingCachedTokens)
	if !changed {
		return false
	}

	usage.PromptTokens = totalInputTokens - cachedTokens
	usage.InputTokens = usage.PromptTokens
	usage.PromptTokensDetails.CachedTokens = cachedTokens
	if usage.InputTokensDetails != nil {
		usage.InputTokensDetails.CachedTokens = cachedTokens
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return true
}

func buildMessageDeltaPatchUsage(claudeResponse *dto.ClaudeResponse, claudeInfo *ClaudeResponseInfo) *dto.ClaudeUsage {
	usage := &dto.ClaudeUsage{}
	if claudeResponse != nil && claudeResponse.Usage != nil {
		*usage = *claudeResponse.Usage
		if usage.InputTokens == 0 {
			usage.InputTokens = claudeResponse.Usage.GetInputTokens()
		}
		if usage.OutputTokens == 0 {
			usage.OutputTokens = claudeResponse.Usage.GetOutputTokens()
		}
		if usage.CacheReadInputTokens == 0 {
			usage.CacheReadInputTokens = claudeResponse.Usage.GetCacheReadInputTokens()
		}
		if usage.CacheCreationInputTokens == 0 {
			usage.CacheCreationInputTokens = claudeResponse.Usage.GetCacheCreationTotalTokens()
		}
	}

	if claudeInfo == nil || claudeInfo.Usage == nil {
		return usage
	}

	if usage.InputTokens == 0 && claudeInfo.Usage.PromptTokens > 0 {
		usage.InputTokens = claudeInfo.Usage.PromptTokens
	}
	if usage.CacheReadInputTokens == 0 && claudeInfo.Usage.PromptTokensDetails.CachedTokens > 0 {
		usage.CacheReadInputTokens = claudeInfo.Usage.PromptTokensDetails.CachedTokens
	}
	if usage.CacheCreationInputTokens == 0 && claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens > 0 {
		usage.CacheCreationInputTokens = claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens
	}
	cacheCreation5m := 0
	cacheCreation1h := 0
	if usage.CacheCreation != nil {
		cacheCreation5m = usage.CacheCreation.Ephemeral5mInputTokens
		cacheCreation1h = usage.CacheCreation.Ephemeral1hInputTokens
	} else {
		cacheCreation5m = claudeInfo.Usage.ClaudeCacheCreation5mTokens
		cacheCreation1h = claudeInfo.Usage.ClaudeCacheCreation1hTokens
	}
	cacheCreation5m, cacheCreation1h = service.NormalizeCacheCreationSplit(
		usage.CacheCreationInputTokens,
		cacheCreation5m,
		cacheCreation1h,
	)
	if usage.CacheCreation == nil && (cacheCreation5m > 0 || cacheCreation1h > 0) {
		usage.CacheCreation = &dto.ClaudeCacheCreationUsage{}
	}
	if usage.CacheCreation != nil {
		usage.CacheCreation.Ephemeral5mInputTokens = cacheCreation5m
		usage.CacheCreation.Ephemeral1hInputTokens = cacheCreation1h
	}
	return usage
}

func shouldSkipClaudeMessageDeltaUsagePatch(info *relaycommon.RelayInfo) bool {
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled {
		return true
	}
	if info == nil {
		return false
	}
	return info.ChannelSetting.PassThroughBodyEnabled
}

func patchClaudeMessageDeltaUsageData(data string, usage *dto.ClaudeUsage) string {
	if data == "" || usage == nil {
		return data
	}

	data = setMessageDeltaUsageInt(data, "usage.input_tokens", usage.InputTokens)
	data = setMessageDeltaUsageInt(data, "usage.cache_read_input_tokens", usage.CacheReadInputTokens)
	data = setMessageDeltaUsageInt(data, "usage.cache_creation_input_tokens", usage.CacheCreationInputTokens)

	if usage.CacheCreation != nil {
		data = setMessageDeltaUsageInt(data, "usage.cache_creation.ephemeral_5m_input_tokens", usage.CacheCreation.Ephemeral5mInputTokens)
		data = setMessageDeltaUsageInt(data, "usage.cache_creation.ephemeral_1h_input_tokens", usage.CacheCreation.Ephemeral1hInputTokens)
	}

	return data
}

func rewriteClaudeUsageData(data string, usage *dto.ClaudeUsage) string {
	if data == "" || usage == nil {
		return data
	}

	var rawData map[string]interface{}
	if err := common.UnmarshalJsonStr(data, &rawData); err != nil {
		return data
	}
	usageMap, ok := rawData["usage"].(map[string]interface{})
	if !ok {
		return data
	}
	applyClaudeUsageToMap(usageMap, usage)
	rawData["usage"] = usageMap
	modifiedData, err := common.Marshal(rawData)
	if err != nil {
		return data
	}
	return string(modifiedData)
}

func rewriteClaudeMessageStartUsageData(data string, usage *dto.ClaudeUsage) string {
	if data == "" || usage == nil {
		return data
	}

	var rawData map[string]interface{}
	if err := common.UnmarshalJsonStr(data, &rawData); err != nil {
		return data
	}
	messageMap, ok := rawData["message"].(map[string]interface{})
	if !ok {
		return data
	}
	usageMap, ok := messageMap["usage"].(map[string]interface{})
	if !ok {
		return data
	}
	applyClaudeUsageToMap(usageMap, usage)
	messageMap["usage"] = usageMap
	rawData["message"] = messageMap
	modifiedData, err := common.Marshal(rawData)
	if err != nil {
		return data
	}
	return string(modifiedData)
}

func applyClaudeUsageToMap(usageMap map[string]interface{}, usage *dto.ClaudeUsage) {
	if usage.InputTokens > 0 {
		usageMap["input_tokens"] = usage.InputTokens
	}
	if usage.OutputTokens > 0 {
		usageMap["output_tokens"] = usage.OutputTokens
	}
	if usage.CacheReadInputTokens > 0 || usage.InputTokens > 0 {
		usageMap["cache_read_input_tokens"] = usage.CacheReadInputTokens
	}
	if usage.CacheCreationInputTokens > 0 {
		usageMap["cache_creation_input_tokens"] = usage.CacheCreationInputTokens
	}
}

func setMessageDeltaUsageInt(data string, path string, localValue int) string {
	if localValue <= 0 {
		return data
	}

	upstreamValue := gjson.Get(data, path)
	if upstreamValue.Exists() && upstreamValue.Int() > 0 {
		return data
	}

	patchedData, err := sjson.Set(data, path, localValue)
	if err != nil {
		return data
	}
	return patchedData
}

func FormatClaudeResponseInfo(claudeResponse *dto.ClaudeResponse, oaiResponse *dto.ChatCompletionsStreamResponse, claudeInfo *ClaudeResponseInfo) bool {
	if claudeInfo == nil {
		return false
	}
	if claudeInfo.Usage == nil {
		claudeInfo.Usage = &dto.Usage{}
	}
	if claudeResponse.Type == "message_start" {
		if claudeResponse.Message != nil {
			claudeInfo.ResponseId = claudeResponse.Message.Id
			claudeInfo.Model = claudeResponse.Message.Model
		}

		// message_start, 获取usage
		if claudeResponse.Message != nil && claudeResponse.Message.Usage != nil {
			mergeClaudeUsageIntoRelayUsage(claudeInfo.Usage, claudeResponse.Message.Usage)
		}
	} else if claudeResponse.Type == "content_block_delta" {
		if claudeResponse.Delta != nil {
			if claudeResponse.Delta.Text != nil {
				text := *claudeResponse.Delta.Text
				claudeInfo.ResponseText.WriteString(text)
				claudeInfo.OutputText.WriteString(text)
			}
			if claudeResponse.Delta.Thinking != nil {
				claudeInfo.ResponseText.WriteString(*claudeResponse.Delta.Thinking)
				claudeInfo.ReasoningText.WriteString(*claudeResponse.Delta.Thinking)
			}
		}
	} else if claudeResponse.Type == "message_delta" {
		// 最终的usage获取
		if claudeResponse.Usage != nil {
			mergeClaudeUsageIntoRelayUsage(claudeInfo.Usage, claudeResponse.Usage)
		}

		// 判断是否完整
		claudeInfo.Done = true
	} else if claudeResponse.Type == "content_block_start" {
	} else {
		return false
	}
	if oaiResponse != nil {
		oaiResponse.Id = claudeInfo.ResponseId
		oaiResponse.Created = claudeInfo.Created
		oaiResponse.Model = claudeInfo.Model
	}
	return true
}

func HandleStreamResponseData(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, data string) *types.NewAPIError {
	var claudeResponse dto.ClaudeResponse
	err := common.UnmarshalJsonStr(data, &claudeResponse)
	if err != nil {
		common.SysLog("error unmarshalling stream response: " + err.Error())
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if claudeError := claudeResponse.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
		return types.WithClaudeError(*claudeError, http.StatusInternalServerError)
	}
	if claudeResponse.StopReason != "" {
		maybeMarkClaudeRefusal(c, claudeResponse.StopReason)
	}
	if claudeResponse.Delta != nil && claudeResponse.Delta.StopReason != nil {
		maybeMarkClaudeRefusal(c, *claudeResponse.Delta.StopReason)
	}
	if info.RelayFormat == types.RelayFormatClaude {
		FormatClaudeResponseInfo(&claudeResponse, nil, claudeInfo)

		if claudeResponse.Type == "message_start" {
			if claudeResponse.Message != nil {
				info.UpstreamModelName = claudeResponse.Message.Model
				if claudeResponse.Message.Usage != nil && service.NormalizeNoCacheUsageForRelay(c, info, claudeInfo.Usage) {
					syncClaudeUsageFieldsFromRelayUsage(claudeResponse.Message.Usage, claudeInfo.Usage)
					data = rewriteClaudeMessageStartUsageData(data, claudeResponse.Message.Usage)
				}
			}
		} else if claudeResponse.Type == "message_delta" {
			if claudeInfo.Usage != nil && claudeInfo.Usage.PromptTokens == 0 {
				claudeInfo.Usage.PromptTokens = info.GetEstimatePromptTokens()
			}
			normalizedInclusiveUsage := false
			if claudeResponse.Usage != nil {
				normalizedInclusiveUsage = normalizeAnthropicInclusiveCacheUsage(info, claudeInfo.Usage)
				syncClaudeUsageFieldsFromRelayUsage(claudeResponse.Usage, claudeInfo.Usage)
				if normalizedInclusiveUsage {
					data = rewriteClaudeUsageData(data, claudeResponse.Usage)
				}
			}
			// 默认仅在上游无缓存时注入；开启补足开关后只提高较低的上游缓存。
			if info.ShouldInjectCacheInfo && claudeResponse.Usage != nil && injectSyntheticCacheInfoForClaudeUsage(claudeInfo.Usage, info) {
				syncClaudeUsageFieldsFromRelayUsage(claudeResponse.Usage, claudeInfo.Usage)
				data = rewriteClaudeUsageData(data, claudeResponse.Usage)
			}
			if service.NormalizeNoCacheUsageForRelay(c, info, claudeInfo.Usage) && claudeResponse.Usage != nil {
				syncClaudeUsageFieldsFromRelayUsage(claudeResponse.Usage, claudeInfo.Usage)
				data = rewriteClaudeUsageData(data, claudeResponse.Usage)
			}
			service.FillMissingReasoningTokens(c, claudeInfo.Usage, claudeInfo.ReasoningText.String(), claudeInfo.OutputText.String(), info.UpstreamModelName)
			// 确保 message_delta 的 usage 包含完整的 input_tokens 和 cache 相关字段
			// 解决 AWS Bedrock 等上游返回的 message_delta 缺少这些字段的问题
			if !shouldSkipClaudeMessageDeltaUsagePatch(info) {
				data = patchClaudeMessageDeltaUsageData(data, buildMessageDeltaPatchUsage(&claudeResponse, claudeInfo))
			}
		}
		helper.ClaudeChunkData(c, claudeResponse, data)
	} else if info.RelayFormat == types.RelayFormatOpenAI {
		response := StreamResponseClaude2OpenAI(&claudeResponse)

		if !FormatClaudeResponseInfo(&claudeResponse, response, claudeInfo) {
			return nil
		}

		err = helper.ObjectData(c, response)
		if err != nil {
			logger.LogError(c, "send_stream_response_failed: "+err.Error())
		}
	}
	return nil
}

func HandleStreamFinalResponse(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo) {
	if claudeInfo.ResponseId != "" {
		info.UpstreamResponseId = claudeInfo.ResponseId
	}
	if claudeInfo.Usage.CompletionTokens == 0 || !claudeInfo.Done {
		if common.DebugEnabled {
			common.SysLog("claude response usage is not complete, maybe upstream error")
		}
		fallback := service.ResponseText2Usage(c, claudeInfo.ResponseText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		if claudeInfo.Usage.CompletionTokens == 0 ||
			(!claudeInfo.Done && fallback.CompletionTokens > claudeInfo.Usage.CompletionTokens) {
			claudeInfo.Usage.CompletionTokens = fallback.CompletionTokens
		}
		if claudeInfo.Usage.PromptTokens == 0 {
			claudeInfo.Usage.PromptTokens = fallback.PromptTokens
		}
		claudeInfo.Usage.TotalTokens = claudeInfo.Usage.PromptTokens + claudeInfo.Usage.CompletionTokens
	}
	if claudeInfo.Usage.PromptTokens == 0 {
		claudeInfo.Usage.PromptTokens = info.GetEstimatePromptTokens()
		common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
	}
	claudeInfo.Usage.TotalTokens = claudeInfo.Usage.PromptTokens + claudeInfo.Usage.CompletionTokens
	if claudeInfo.Usage != nil {
		claudeInfo.Usage.UsageSemantic = "anthropic"
	}
	normalizeAnthropicInclusiveCacheUsage(info, claudeInfo.Usage)

	// 默认仅在上游无缓存时注入；开启补足开关后只提高较低的上游缓存。
	if info.ShouldInjectCacheInfo && claudeInfo.Usage.PromptTokens+claudeInfo.Usage.PromptTokensDetails.CachedTokens >= 4096 {
		injectSyntheticCacheInfoForClaudeUsage(claudeInfo.Usage, info)
	}
	service.NormalizeNoCacheUsageForRelay(c, info, claudeInfo.Usage)
	service.FillMissingReasoningTokens(c, claudeInfo.Usage, claudeInfo.ReasoningText.String(), claudeInfo.OutputText.String(), info.UpstreamModelName)

	if info.RelayFormat == types.RelayFormatClaude {
		//
	} else if info.RelayFormat == types.RelayFormatOpenAI {
		if info.ShouldIncludeUsage {
			openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(claudeInfo.Usage)
			response := helper.GenerateFinalUsageResponse(claudeInfo.ResponseId, claudeInfo.Created, info.UpstreamModelName, openAIUsage)
			err := helper.ObjectData(c, response)
			if err != nil {
				common.SysLog("send final response failed: " + err.Error())
			}
		}
		helper.Done(c)
	}
}

func ClaudeStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	claudeInfo := &ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	var err *types.NewAPIError
	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		err = HandleStreamResponseData(c, info, claudeInfo, data)
		if err != nil {
			sr.Stop(err)
		}
	})
	if err != nil {
		return nil, err
	}

	HandleStreamFinalResponse(c, info, claudeInfo)
	return claudeInfo.Usage, nil
}

func HandleClaudeResponseData(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, httpResp *http.Response, data []byte) *types.NewAPIError {
	var claudeResponse dto.ClaudeResponse
	err := common.Unmarshal(data, &claudeResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if claudeError := claudeResponse.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
		return types.WithClaudeError(*claudeError, http.StatusInternalServerError)
	}
	maybeMarkClaudeRefusal(c, claudeResponse.StopReason)
	if claudeResponse.Id != "" {
		info.UpstreamResponseId = claudeResponse.Id
	}
	if claudeInfo.Usage == nil {
		claudeInfo.Usage = &dto.Usage{}
	}
	responseText, reasoningText, outputText := collectClaudeResponseTextAndReasoning(&claudeResponse)
	noCacheNormalized := false
	if claudeResponse.Usage != nil {
		mergeClaudeUsageIntoRelayUsage(claudeInfo.Usage, claudeResponse.Usage)

		if claudeInfo.Usage.PromptTokens == 0 || claudeInfo.Usage.CompletionTokens == 0 {
			service.EnsureCompleteUsage(c, claudeInfo.Usage, info.GetEstimatePromptTokens(), responseText, info.UpstreamModelName)
			claudeResponse.Usage.InputTokens = claudeInfo.Usage.PromptTokens
			claudeResponse.Usage.OutputTokens = claudeInfo.Usage.CompletionTokens
		}
		normalizedInclusiveUsage := normalizeAnthropicInclusiveCacheUsage(info, claudeInfo.Usage)
		if normalizedInclusiveUsage || claudeResponse.Usage.CacheReadInputTokens == 0 || claudeResponse.Usage.CacheCreationInputTokens == 0 {
			syncClaudeUsageFieldsFromRelayUsage(claudeResponse.Usage, claudeInfo.Usage)
		}

		// 默认仅在上游无缓存时注入；开启补足开关后只提高较低的上游缓存。
		if info.ShouldInjectCacheInfo && injectSyntheticCacheInfoForClaudeUsage(claudeInfo.Usage, info) {
			syncClaudeUsageFieldsFromRelayUsage(claudeResponse.Usage, claudeInfo.Usage)
		}
		noCacheNormalized = service.NormalizeNoCacheUsageForRelay(c, info, claudeInfo.Usage)
		if noCacheNormalized {
			syncClaudeUsageFieldsFromRelayUsage(claudeResponse.Usage, claudeInfo.Usage)
		}
	}
	service.FillMissingReasoningTokens(c, claudeInfo.Usage, reasoningText, outputText, info.UpstreamModelName)
	var responseData []byte
	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		openaiResponse := ResponseClaude2OpenAI(&claudeResponse)
		openaiResponse.Usage = buildOpenAIStyleUsageFromClaudeUsage(claudeInfo.Usage)
		responseData, err = common.Marshal(openaiResponse)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
	case types.RelayFormatClaude:
		// 如果注入了缓存信息，需要修改原始响应数据
		if claudeResponse.Usage != nil && (claudeInfo.Usage.UsageSource == normalizedAnthropicInclusiveCacheUsageSource || info.ShouldInjectCacheInfo && claudeResponse.Usage.CacheReadInputTokens > 0 || noCacheNormalized) {
			var rawData map[string]interface{}
			if err := common.Unmarshal(data, &rawData); err == nil {
				if usageMap, ok := rawData["usage"].(map[string]interface{}); ok {
					usageMap["input_tokens"] = claudeResponse.Usage.InputTokens
					usageMap["cache_read_input_tokens"] = claudeResponse.Usage.CacheReadInputTokens
					rawData["usage"] = usageMap
					responseData, _ = common.Marshal(rawData)
				} else {
					responseData = data
				}
			} else {
				responseData = data
			}
		} else {
			responseData = data
		}
	}

	if claudeResponse.Usage != nil && claudeResponse.Usage.ServerToolUse != nil && claudeResponse.Usage.ServerToolUse.WebSearchRequests > 0 {
		c.Set("claude_web_search_requests", claudeResponse.Usage.ServerToolUse.WebSearchRequests)
	}

	service.IOCopyBytesGracefully(c, httpResp, responseData)
	return nil
}

func ClaudeHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	claudeInfo := &ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if common.DebugEnabled {
		println("responseBody: ", string(responseBody))
	}
	handleErr := HandleClaudeResponseData(c, info, claudeInfo, resp, responseBody)
	if handleErr != nil {
		return nil, handleErr
	}
	return claudeInfo.Usage, nil
}

func mapToolChoice(toolChoice any, parallelToolCalls *bool) *dto.ClaudeToolChoice {
	var claudeToolChoice *dto.ClaudeToolChoice

	// 处理 tool_choice 字符串值
	if toolChoiceStr, ok := toolChoice.(string); ok {
		switch toolChoiceStr {
		case "auto":
			claudeToolChoice = &dto.ClaudeToolChoice{
				Type: "auto",
			}
		case "required":
			claudeToolChoice = &dto.ClaudeToolChoice{
				Type: "any",
			}
		case "none":
			claudeToolChoice = &dto.ClaudeToolChoice{
				Type: "none",
			}
		}
	} else if toolChoiceMap, ok := toolChoice.(map[string]interface{}); ok {
		// 处理 tool_choice 对象值
		if function, ok := toolChoiceMap["function"].(map[string]interface{}); ok {
			if toolName, ok := function["name"].(string); ok {
				claudeToolChoice = &dto.ClaudeToolChoice{
					Type: "tool",
					Name: toolName,
				}
			}
		}
	}

	// 处理 parallel_tool_calls
	if parallelToolCalls != nil {
		if claudeToolChoice == nil {
			// 如果没有 tool_choice，但有 parallel_tool_calls，创建默认的 auto 类型
			claudeToolChoice = &dto.ClaudeToolChoice{
				Type: "auto",
			}
		}

		// Anthropic schema: tool_choice.type=none does not accept extra fields.
		// When tools are disabled, parallel_tool_calls is irrelevant, so we drop it.
		if claudeToolChoice.Type != "none" {
			// 如果 parallel_tool_calls 为 true，则 disable_parallel_tool_use 为 false
			claudeToolChoice.DisableParallelToolUse = !*parallelToolCalls
		}
	}

	return claudeToolChoice
}
