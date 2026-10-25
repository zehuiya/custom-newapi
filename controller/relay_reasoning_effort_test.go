package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTokenLimitReselectionPreservesOriginalReasoningEffort(t *testing.T) {
	for _, test := range []struct{ path, body string }{
		{"/v1/chat/completions", `{"reasoning_effort":"max"}`},
		{"/v1/responses", `{"reasoning":{"effort":"max"}}`},
		{"/v1/messages", `{"output_config":{"effort":"max"}}`},
	} {
		t.Run(test.path, func(t *testing.T) {
			db := setupRelayEndpointTestDB(t)
			high := `{"reasoning_effort_filter":{"allowed_values":["high"]}}`
			max := `{"reasoning_effort_filter":{"allowed_values":["max"]}}`
			channels := []*model.Channel{{MaxOutputTokens: common.GetPointer(5)}, {Setting: &high}, {Setting: &max}}
			for i, channel := range channels {
				priority := int64(150 - i*50)
				channel.Type, channel.Status, channel.Key, channel.Priority = constant.ChannelTypeOpenAI, common.ChannelStatusEnabled, "test", &priority
				require.NoError(t, db.Create(channel).Error)
				require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "effort-model", ChannelId: channel.Id, Enabled: true, Priority: &priority}).Error)
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			c.Request.Header.Set("Content-Type", "application/json")
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			require.NoError(t, service.CaptureRequestReasoningEffort(c))
			common.SetContextKey(c, constant.ContextKeyChannelId, channels[0].Id)
			// Simulate an adapter or parameter override changing the upstream body.
			c.Request.Body = io.NopCloser(strings.NewReader(`{"reasoning_effort":"high"}`))
			info := &relaycommon.RelayInfo{TokenGroup: "default", OriginModelName: "effort-model", ReasoningEffort: "high"}
			require.Nil(t, ensureSelectedChannelSatisfiesTokenLimit(c, info, 10, 20))
			require.Equal(t, channels[2].Id, common.GetContextKeyInt(c, constant.ContextKeyChannelId))
		})
	}
}
