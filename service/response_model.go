package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// RestoreResponseModel rewrites only protocol model fields in the outgoing
// payload. Upstream model names used for routing and accounting stay unchanged.
func RestoreResponseModel(c *gin.Context, data []byte) []byte {
	model := restoredResponseModelName(c)
	if model == "" {
		return data
	}
	return restoreResponseModel(data, model)
}

// RestoreResponseModelString avoids copying untouched SSE payloads while the
// channel's response model option is disabled.
func RestoreResponseModelString(c *gin.Context, data string) string {
	model := restoredResponseModelName(c)
	if model == "" {
		return data
	}
	return string(restoreResponseModel([]byte(data), model))
}

func restoredResponseModelName(c *gin.Context) string {
	if c == nil {
		return ""
	}
	setting, ok := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	if !ok || !setting.RestoreResponseModel {
		return ""
	}
	model := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
	if _, name, found := strings.Cut(model, ":"); found && name != "" {
		return name
	}
	return model
}

func restoreResponseModel(data []byte, model string) []byte {
	return rewriteResponseFields(data, model, "")
}

// RewriteRelayResponse changes only outgoing metadata. Upstream identifiers
// used for accounting and stateful Responses requests are left intact.
func RewriteRelayResponse(c *gin.Context, data []byte) []byte {
	model, id := restoredResponseModelName(c), downstreamResponseID(c)
	if model == "" && id == "" {
		return data
	}
	return rewriteResponseFields(data, model, id)
}

func RewriteRelayResponseString(c *gin.Context, data string) string {
	model, id := restoredResponseModelName(c), downstreamResponseID(c)
	if model == "" && id == "" {
		return data
	}
	return string(rewriteResponseFields([]byte(data), model, id))
}

func downstreamResponseID(c *gin.Context) string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return ""
	}
	path := strings.TrimSuffix(c.Request.URL.Path, "/")
	if !strings.HasSuffix(path, "/chat/completions") && !strings.HasSuffix(path, "/messages") {
		return ""
	}
	return c.GetString(common.RequestIdKey)
}

func rewriteResponseFields(data []byte, model, id string) []byte {
	if !gjson.ValidBytes(data) {
		return data
	}
	response := gjson.ParseBytes(data)
	if !response.IsObject() || response.Get("type").String() == "error" {
		return data
	}
	if upstreamError := response.Get("error"); upstreamError.Exists() && upstreamError.Type != gjson.Null {
		return data
	}

	paths := []string{"model", "modelVersion"}
	switch eventType := response.Get("type").String(); {
	case eventType == "message_start":
		paths = append(paths, "message.model")
	case strings.HasPrefix(eventType, "response."):
		paths = append(paths, "response.model")
	case eventType == "session.created" || eventType == "session.updated":
		paths = append(paths, "session.model")
	}
	for _, path := range paths {
		value := response.Get(path)
		if model == "" || value.Type != gjson.String || value.Str == model {
			continue
		}
		patched, err := sjson.SetBytes(data, path, model)
		if err != nil {
			return data
		}
		data = patched
	}
	if id != "" {
		path := "id"
		if response.Get("type").String() == "message_start" {
			if !response.Get("message").IsObject() {
				return data
			}
			path = "message.id"
		} else if response.Get("id").Type != gjson.String &&
			!response.Get("choices").IsArray() && response.Get("type").String() != "message" {
			return data
		}
		if response.Get(path).String() != id {
			if patched, err := sjson.SetBytes(data, path, id); err == nil {
				data = patched
			}
		}
	}
	return data
}
