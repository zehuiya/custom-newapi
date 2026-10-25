package service

import (
	"encoding/json"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
)

// CaptureRequestReasoningEffort freezes routing metadata before channel-specific
// conversions. The reusable body remains intact for the relay and retries.
func CaptureRequestReasoningEffort(c *gin.Context) error {
	if !dto.IsChannelEndpointPath(c.Request.URL.Path) {
		return nil
	}
	if _, exists := common.GetContextKey(c, constant.ContextKeyRequestReasoningEffort); exists {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := common.UnmarshalBodyReusable(c, &fields); err != nil {
		return err
	}
	effort := &dto.RequestReasoningEffort{}
	field := "reasoning_effort"
	parent := ""
	switch c.Request.URL.Path {
	case "/v1/responses":
		parent, field = "reasoning", "effort"
	case "/v1/messages":
		parent, field = "output_config", "effort"
	}
	if parent != "" {
		if raw, exists := fields[parent]; exists {
			var nested map[string]json.RawMessage
			if err := common.Unmarshal(raw, &nested); err != nil || nested == nil {
				effort.Present = true
				common.SetContextKey(c, constant.ContextKeyRequestReasoningEffort, effort)
				return nil
			}
			fields = nested
		} else {
			fields = nil
		}
	}
	if raw, exists := fields[field]; exists {
		effort.Present = true
		// A failed string decode (including null) remains present and empty.
		// This preserves legacy handling on unrestricted channels without
		// allowing malformed values to bypass a channel's missing-value rule.
		var value *string
		if err := common.Unmarshal(raw, &value); err == nil && value != nil {
			effort.Value = *value
		}
	}
	common.SetContextKey(c, constant.ContextKeyRequestReasoningEffort, effort)
	return nil
}

func GetRequestReasoningEffort(c *gin.Context) *dto.RequestReasoningEffort {
	if c == nil {
		return nil
	}
	value, _ := common.GetContextKey(c, constant.ContextKeyRequestReasoningEffort)
	effort, _ := value.(*dto.RequestReasoningEffort)
	return effort
}
