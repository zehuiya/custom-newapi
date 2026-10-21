package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const distributorEndpointTestModel = "endpoint-test-model"

func setupDistributorEndpointTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousSQLite, previousMySQL, previousPostgreSQL := common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL
	previousSQLitePath, previousMasterNode := common.SQLitePath, common.IsMasterNode
	previousMemoryCache, previousRedis := common.MemoryCacheEnabled, common.RedisEnabled
	previousGinMode := gin.Mode()
	previousAffinity := *operation_setting.GetChannelAffinitySetting()
	previousTranslateMessage := common.TranslateMessage

	t.Setenv("SQL_DSN", "local")
	common.SQLitePath = fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = true, false, false
	common.IsMasterNode = false
	common.MemoryCacheEnabled, common.RedisEnabled = false, false
	operation_setting.GetChannelAffinitySetting().Enabled = false
	gin.SetMode(gin.TestMode)

	// InitDB also initializes the model package's private SQL column quoting.
	// Disabling master-node migration keeps this fixture limited to two tables.
	var testDB *gorm.DB
	t.Cleanup(func() {
		model.DB = previousDB
		common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = previousSQLite, previousMySQL, previousPostgreSQL
		common.SQLitePath, common.IsMasterNode = previousSQLitePath, previousMasterNode
		common.MemoryCacheEnabled, common.RedisEnabled = previousMemoryCache, previousRedis
		*operation_setting.GetChannelAffinitySetting() = previousAffinity
		common.TranslateMessage = previousTranslateMessage
		gin.SetMode(previousGinMode)
		if testDB != nil {
			if sqlDB, err := testDB.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}
	})
	require.NoError(t, model.InitDB())
	testDB = model.DB
	sqlDB, err := testDB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, testDB.AutoMigrate(&model.Channel{}, &model.Ability{}))
	require.NoError(t, i18n.Init())
	return testDB
}

func addDistributorEndpointTestChannel(t *testing.T, db *gorm.DB, setting string, priority int64) *model.Channel {
	t.Helper()
	channel := &model.Channel{
		Type:     constant.ChannelTypeOpenAI,
		Key:      "endpoint-test-key",
		Name:     "endpoint-test-channel",
		Status:   common.ChannelStatusEnabled,
		Models:   distributorEndpointTestModel,
		Group:    "default",
		Priority: &priority,
	}
	if setting != "" {
		channel.Setting = &setting
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(db))
	return channel
}

func resetDistributorEndpointTestChannels(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Ability{}).Error)
	require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Channel{}).Error)
}

func requestDistributorEndpoint(path string, specificChannelID int) (*httptest.ResponseRecorder, int) {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		defer common.CleanupBodyStorage(c)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		c.Set("endpoint_test_affinity", "endpoint-test-session")
		if specificChannelID > 0 {
			common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, strconv.Itoa(specificChannelID))
		}
		c.Next()
	})
	selectedChannelID := 0
	router.POST(path, Distribute(), func(c *gin.Context) {
		selectedChannelID = common.GetContextKeyInt(c, constant.ContextKeyChannelId)
		c.Status(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"`+distributorEndpointTestModel+`","messages":[]}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder, selectedChannelID
}

func requireDistributorEndpointUnavailable(t *testing.T, response *httptest.ResponseRecorder, selectedChannelID int) {
	t.Helper()
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	require.Zero(t, selectedChannelID, "an unavailable endpoint must abort before the relay handler")
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, string(types.ErrorCodeModelNotFound), body.Error.Code)
}

func TestDistributeEndpointSettings(t *testing.T) {
	// These tests replace process-wide database/configuration state and must stay sequential.
	db := setupDistributorEndpointTestDB(t)
	endpoints := []struct {
		path    string
		setting string
	}{
		{"/v1/messages", `{"messages_enabled":false}`},
		{"/v1/chat/completions", `{"chat_completions_enabled":false}`},
		{"/pg/chat/completions", `{"chat_completions_enabled":false}`},
		{"/v1/responses", `{"responses_enabled":false}`},
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint.path, func(t *testing.T) {
			t.Run("disabled_high_priority_is_excluded", func(t *testing.T) {
				resetDistributorEndpointTestChannels(t, db)
				addDistributorEndpointTestChannel(t, db, endpoint.setting, 100)
				enabled := addDistributorEndpointTestChannel(t, db, "", 10)
				response, selected := requestDistributorEndpoint(endpoint.path, 0)
				require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
				require.Equal(t, enabled.Id, selected)
			})
			t.Run("specific_disabled_channel_is_rejected", func(t *testing.T) {
				resetDistributorEndpointTestChannels(t, db)
				disabled := addDistributorEndpointTestChannel(t, db, endpoint.setting, 100)
				addDistributorEndpointTestChannel(t, db, "", 10)
				response, selected := requestDistributorEndpoint(endpoint.path, disabled.Id)
				requireDistributorEndpointUnavailable(t, response, selected)
			})
			t.Run("all_matching_channels_disabled", func(t *testing.T) {
				resetDistributorEndpointTestChannels(t, db)
				addDistributorEndpointTestChannel(t, db, endpoint.setting, 100)
				addDistributorEndpointTestChannel(t, db, endpoint.setting, 10)
				response, selected := requestDistributorEndpoint(endpoint.path, 0)
				requireDistributorEndpointUnavailable(t, response, selected)
			})
			t.Run("other_endpoint_switches_are_independent", func(t *testing.T) {
				resetDistributorEndpointTestChannels(t, db)
				channel := addDistributorEndpointTestChannel(t, db, endpoint.setting, 100)
				for _, other := range endpoints {
					// Playground shares the Chat Completions switch by design.
					if other.setting == endpoint.setting {
						continue
					}
					response, selected := requestDistributorEndpoint(other.path, 0)
					require.Equal(t, http.StatusNoContent, response.Code, "%s: %s", other.path, response.Body.String())
					require.Equal(t, channel.Id, selected)
				}
			})
			for _, setting := range []string{"", "{}"} {
				t.Run("legacy_defaults_enabled_"+setting, func(t *testing.T) {
					resetDistributorEndpointTestChannels(t, db)
					legacy := addDistributorEndpointTestChannel(t, db, setting, 100)
					for _, specificID := range []int{0, legacy.Id} {
						response, selected := requestDistributorEndpoint(endpoint.path, specificID)
						require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
						require.Equal(t, legacy.Id, selected)
					}
				})
			}
		})
	}
}

func TestDistributeEndpointSkipsDisabledAffinityChannel(t *testing.T) {
	db := setupDistributorEndpointTestDB(t)
	for _, endpoint := range []struct {
		path    string
		setting string
	}{
		{"/v1/messages", `{"messages_enabled":false}`},
		{"/v1/chat/completions", `{"chat_completions_enabled":false}`},
		{"/pg/chat/completions", `{"chat_completions_enabled":false}`},
	} {
		t.Run(endpoint.path, func(t *testing.T) {
			resetDistributorEndpointTestChannels(t, db)
			ruleName := strings.ReplaceAll(t.Name(), "/", "_")
			affinity := operation_setting.GetChannelAffinitySetting()
			previousAffinity := *affinity
			*affinity = operation_setting.ChannelAffinitySetting{
				Enabled:           true,
				SwitchOnSuccess:   true,
				MaxEntries:        100,
				DefaultTTLSeconds: 60,
				Rules: []operation_setting.ChannelAffinityRule{{
					Name:               ruleName,
					ModelRegex:         []string{"^" + distributorEndpointTestModel + "$"},
					KeySources:         []operation_setting.ChannelAffinityKeySource{{Type: "context_string", Key: "endpoint_test_affinity"}},
					SkipRetryOnFailure: true,
					IncludeUsingGroup:  true,
					IncludeModelName:   true,
					IncludeRuleName:    true,
				}},
			}
			t.Cleanup(func() {
				_, err := service.ClearChannelAffinityCacheByRuleName(ruleName)
				*affinity = previousAffinity
				require.NoError(t, err)
			})

			preferred := addDistributorEndpointTestChannel(t, db, "", 100)
			response, selected := requestDistributorEndpoint(endpoint.path, 0)
			require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
			require.Equal(t, preferred.Id, selected)

			// Verify a real affinity hit before disabling the selected endpoint.
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest(http.MethodPost, endpoint.path, nil)
			context.Set("endpoint_test_affinity", "endpoint-test-session")
			cachedID, found := service.GetPreferredChannelByAffinity(context, distributorEndpointTestModel, "default")
			require.True(t, found)
			require.Equal(t, preferred.Id, cachedID)

			require.NoError(t, db.Model(preferred).Update("setting", endpoint.setting).Error)
			fallback := addDistributorEndpointTestChannel(t, db, "", 10)
			response, selected = requestDistributorEndpoint(endpoint.path, 0)
			require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
			require.Equal(t, fallback.Id, selected, "affinity must not bypass the endpoint switch")
		})
	}
}
