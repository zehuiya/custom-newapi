package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestReasoningEffortAppliesToFallbackRetryAndAutoGroups(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))
	high := `{"reasoning_effort_filter":{"allowed_values":["high"],"allow_missing":true}}`
	max := `{"reasoning_effort_filter":{"allowed_values":["max","future-v3"]}}`
	channels := []model.Channel{
		{Id: 82001, Key: "test", Group: "effort-group-a", Models: "fallback-model,auto-model", Status: common.ChannelStatusEnabled, Priority: common.GetPointer(int64(100)), Setting: &high},
		{Id: 82002, Key: "test", Group: "effort-group-a", Models: "fallback-model", Status: common.ChannelStatusEnabled, Priority: common.GetPointer(int64(50)), Setting: &max, MaxOutputTokens: common.GetPointer(100)},
		{Id: 82003, Key: "test", Group: "effort-group-b", Models: "auto-model", Status: common.ChannelStatusEnabled, Priority: common.GetPointer(int64(100)), Setting: &max},
	}
	oldCache := common.MemoryCacheEnabled
	oldAuto, oldUsable := setting.AutoGroups2JsonString(), setting.UserUsableGroups2JSONString()
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("channel_id in ?", []int{82001, 82002, 82003}).Delete(&model.Ability{}).Error)
		require.NoError(t, model.DB.Where("id in ?", []int{82001, 82002, 82003}).Delete(&model.Channel{}).Error)
		model.InitChannelCache()
		common.MemoryCacheEnabled = oldCache
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(oldAuto))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldUsable))
	})
	require.NoError(t, model.DB.Create(&channels).Error)
	for i := range channels {
		require.NoError(t, channels[i].AddAbilities(model.DB))
	}
	model.InitChannelCache()
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["effort-group-a","effort-group-b"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"effort-group-a":"A","effort-group-b":"B"}`))

	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		t.Run(path, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, path, nil)
			common.SetContextKey(c, constant.ContextKeyRequestReasoningEffort, &dto.RequestReasoningEffort{Present: true, Value: "max"})
			common.SetContextKey(c, constant.ContextKeyUserGroup, "effort-group-a")
			param := &RetryParam{Ctx: c, TokenGroup: "effort-group-a", ModelName: "fallback-model", RequestPath: path}
			param.ConfigureFallback(1, []int{82001, 82002}, "effort-group-a")
			channel, err := CacheGetFallbackSatisfiedChannel(param, 82001)
			require.ErrorContains(t, err, "does not satisfy request constraints")
			require.Nil(t, channel)
			channel, err = CacheGetFallbackSatisfiedChannel(param, 82002)
			require.NoError(t, err)
			require.Equal(t, 82002, channel.Id)
			param.SetRetry(1)
			channel, _, err = CacheGetRandomSatisfiedChannel(param)
			require.NoError(t, err)
			require.Equal(t, 82002, channel.Id)
			param.TokenLimit = &model.ChannelTokenLimit{MaxTokens: 101}
			channel, err = CacheGetFallbackSatisfiedChannel(param, 82002)
			require.Error(t, err)
			require.Nil(t, channel, "reasoning and token constraints both apply")

			auto := &RetryParam{Ctx: c, TokenGroup: "auto", ModelName: "auto-model", RequestPath: path}
			channel, group, err := CacheGetRandomSatisfiedChannel(auto)
			require.NoError(t, err)
			require.NotNil(t, channel)
			require.Equal(t, 82003, channel.Id)
			require.Equal(t, "effort-group-b", group, "an incompatible first group cannot stop automatic selection")
		})
	}
}
