package controller

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRelayOpenAIContentFilterRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const modelName = "test-content-filter"
	const filtered = `{"id":"filtered-attempt-id","choices":[{"index":0,"finish_reason":"content_filter","message":{"role":"assistant","content":"upstream refusal"}}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`
	const completed = `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"fallback answer"}}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`
	const failed = `{"error":{"type":"upstream_error","code":"bad_gateway","message":"fallback failure"}}`
	const filteredSSE = "data: {\"id\":\"stream-id\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"upstream refusal\"},\"finish_reason\":\"content_filter\"}]}\n\n" +
		"data: {\"id\":\"stream-id\",\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"total_tokens\":10}}\n\n" +
		"data: [DONE]\n\n"

	previousPrices := ratio_setting.ModelPrice2JSONString()
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"test-content-filter":0}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices)) })
	quotaSettings := operation_setting.GetQuotaSetting()
	previousPreConsume := quotaSettings.EnableFreeModelPreConsume
	previousRetryTimes, previousStreamTimeout := common.RetryTimes, constant.StreamingTimeout
	previousConsumeLog, previousErrorLog := common.LogConsumeEnabled, constant.ErrorLogEnabled
	previousRedis, previousBatchUpdate, previousExport := common.RedisEnabled, common.BatchUpdateEnabled, common.DataExportEnabled
	quotaSettings.EnableFreeModelPreConsume = false
	common.RetryTimes, constant.StreamingTimeout = 1, 30
	common.LogConsumeEnabled, constant.ErrorLogEnabled = true, true
	common.RedisEnabled, common.BatchUpdateEnabled, common.DataExportEnabled = false, false, false
	t.Cleanup(func() {
		quotaSettings.EnableFreeModelPreConsume = previousPreConsume
		common.RetryTimes, constant.StreamingTimeout = previousRetryTimes, previousStreamTimeout
		common.LogConsumeEnabled, constant.ErrorLogEnabled = previousConsumeLog, previousErrorLog
		common.RedisEnabled, common.BatchUpdateEnabled, common.DataExportEnabled = previousRedis, previousBatchUpdate, previousExport
	})
	service.InitHttpClient()
	t.Cleanup(service.GetHttpClient().CloseIdleConnections)

	for _, tt := range []struct {
		name       string
		stream     bool
		secondCode int
		secondBody string
		wantStatus int
	}{
		{name: "second channel succeeds", secondCode: http.StatusOK, secondBody: completed, wantStatus: http.StatusOK},
		{name: "both channels filtered", secondCode: http.StatusOK, secondBody: filtered, wantStatus: http.StatusTooManyRequests},
		{name: "fallback fails differently", secondCode: http.StatusBadGateway, secondBody: failed, wantStatus: http.StatusTooManyRequests},
		{name: "stream is not retried", stream: true, secondCode: http.StatusOK, secondBody: completed, wantStatus: http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db := setupRelayEndpointTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}))
			previousLogDB := model.LOG_DB
			model.LOG_DB = db
			t.Cleanup(func() { model.LOG_DB = previousLogDB })
			user := model.User{Username: "filter-test-user"}
			require.NoError(t, db.Create(&user).Error)

			newChannel := func(priority int64, status int, contentType, body string) (*model.Channel, *atomic.Int32) {
				t.Helper()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					calls.Add(1)
					w.Header().Set("Content-Type", contentType)
					w.WriteHeader(status)
					_, _ = io.WriteString(w, body)
				}))
				t.Cleanup(server.Close)
				channel := &model.Channel{
					Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
					Key: "test-key", BaseURL: &server.URL, Priority: &priority,
				}
				require.NoError(t, db.Create(channel).Error)
				require.NoError(t, db.Create(&model.Ability{
					Group: "default", Model: modelName, ChannelId: channel.Id, Enabled: true, Priority: &priority,
				}).Error)
				return channel, &calls
			}
			contentType, firstBody := "application/json", filtered
			if tt.stream {
				contentType, firstBody = "text/event-stream", filteredSSE
			}
			first, firstCalls := newChannel(2, http.StatusOK, contentType, firstBody)
			second, secondCalls := newChannel(1, tt.secondCode, "application/json", tt.secondBody)
			first.StatusCodeMapping = common.GetPointer(`{"429":400}`)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(
				`{"model":%q,"stream":%t,"messages":[{"role":"user","content":"test"}]}`, modelName, tt.stream)))
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyUserId, user.Id)
			common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
			common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
			require.Nil(t, middleware.SetupContextForSelectedChannel(c, first, modelName))

			Relay(c, types.RelayFormatOpenAI)

			require.Equal(t, tt.wantStatus, w.Code, w.Body.String())
			require.EqualValues(t, 1, firstCalls.Load())
			if tt.stream {
				require.Zero(t, secondCalls.Load())
				require.Contains(t, w.Body.String(), `"finish_reason":"content_filter"`)
				return
			}
			require.EqualValues(t, 1, secondCalls.Load())
			require.NotContains(t, w.Body.String(), "upstream refusal")
			var firstError model.Log
			require.NoError(t, db.Where("type = ? AND channel_id = ?", model.LogTypeError, first.Id).First(&firstError).Error)
			require.Equal(t, "content_filter", gjson.Get(firstError.Other, "error_code").String())
			require.EqualValues(t, http.StatusTooManyRequests, gjson.Get(firstError.Other, "status_code").Int())
			var consumeLogs []model.Log
			require.NoError(t, db.Where("type = ?", model.LogTypeConsume).Find(&consumeLogs).Error)
			if tt.wantStatus == http.StatusOK {
				require.Equal(t, "fallback answer", gjson.Get(w.Body.String(), "choices.0.message.content").String())
				require.Len(t, consumeLogs, 1)
				require.Equal(t, second.Id, consumeLogs[0].ChannelId)
				require.Empty(t, gjson.Get(consumeLogs[0].Other, "reject_reason").String())
				require.Empty(t, gjson.Get(consumeLogs[0].Other, "upstream_response_id").String())
			} else {
				require.Equal(t, "content_filter", gjson.Get(w.Body.String(), "error.message").String())
				require.Equal(t, "content_filter", gjson.Get(w.Body.String(), "error.code").String())
				require.Empty(t, consumeLogs)
			}
		})
	}
}
