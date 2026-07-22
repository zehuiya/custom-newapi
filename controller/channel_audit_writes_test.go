package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupChannelAuditWriteTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousSQLite := common.UsingSQLite
	previousMySQL := common.UsingMySQL
	previousPostgreSQL := common.UsingPostgreSQL

	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.ChannelAuditLog{}))
	model.DB = db
	model.LOG_DB = db

	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.UsingSQLite = previousSQLite
		common.UsingMySQL = previousMySQL
		common.UsingPostgreSQL = previousPostgreSQL
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func channelAuditTestActor() model.ChannelAuditActor {
	return model.ChannelAuditActor{
		Type: model.ChannelAuditActorAdmin,
		ID:   7,
		Name: "audit-admin",
		Role: 10,
	}
}

func TestUpdateChannelUpstreamModelSettingsAuditsActualChangeAndRedactsSecrets(t *testing.T) {
	db := setupChannelAuditWriteTestDB(t)

	const rawKey = "sk-audit-secret-key"
	const proxyPassword = "audit-proxy-password"
	settingJSON := `{"proxy":"http://proxy-user:` + proxyPassword + `@127.0.0.1:8080"}`
	channel := &model.Channel{
		Type:        1,
		Key:         rawKey,
		Status:      common.ChannelStatusEnabled,
		Name:        "audit-model-sync",
		Models:      "model-a",
		Group:       "default",
		Setting:     &settingJSON,
		CreatedTime: 1,
	}
	initialSettings := dto.ChannelOtherSettings{
		UpstreamModelUpdateCheckEnabled: true,
	}
	channel.SetOtherSettings(initialSettings)
	require.NoError(t, db.Create(channel).Error)

	updatedSettings := initialSettings
	updatedSettings.UpstreamModelUpdateIgnoredModels = []string{"ignored-model"}
	updatedSettings.UpstreamModelUpdateLastDetectedModels = []string{"pending-model"}
	channel.Models = "model-a,model-b"

	err := updateChannelUpstreamModelSettings(
		channel,
		updatedSettings,
		true,
		channelAuditTestActor(),
		"channel_upstream_apply",
		"batch-model-sync",
	)
	require.NoError(t, err)

	var audit model.ChannelAuditLog
	require.NoError(t, db.First(&audit).Error)
	require.Equal(t, model.ChannelAuditActionModelSync, audit.Action)
	require.Equal(t, "channel_upstream_apply", audit.Source)
	require.Equal(t, "batch-model-sync", audit.BatchId)
	require.Equal(t, "audit-admin", audit.OperatorName)
	require.Greater(t, audit.ChangeCount, 0)

	serializedAudit := strings.Join([]string{
		string(audit.BeforeSnapshot),
		string(audit.AfterSnapshot),
		string(audit.Changes),
		string(audit.ChangedFields),
	}, "\n")
	require.NotContains(t, serializedAudit, rawKey)
	require.NotContains(t, serializedAudit, proxyPassword)
	require.Contains(t, string(audit.Changes), "models")
	require.Contains(t, string(audit.Changes), "upstream_model_update_ignored_models")
	require.Contains(t, string(audit.Changes), "upstream_model_update_last_detected_models")

	var abilities []model.Ability
	require.NoError(t, db.Where("channel_id = ?", channel.Id).Order("model asc").Find(&abilities).Error)
	require.Len(t, abilities, 2)
	require.Equal(t, "model-a", abilities[0].Model)
	require.Equal(t, "model-b", abilities[1].Model)
}

func TestUpdateChannelUpstreamModelSettingsSkipsLastCheckOnlyAudit(t *testing.T) {
	db := setupChannelAuditWriteTestDB(t)

	channel := &model.Channel{
		Type:        1,
		Key:         "sk-runtime-only",
		Status:      common.ChannelStatusEnabled,
		Name:        "audit-runtime-only",
		Models:      "model-a",
		Group:       "default",
		CreatedTime: 1,
	}
	initialSettings := dto.ChannelOtherSettings{
		UpstreamModelUpdateCheckEnabled:  true,
		UpstreamModelUpdateLastCheckTime: 1,
	}
	channel.SetOtherSettings(initialSettings)
	require.NoError(t, db.Create(channel).Error)

	updatedSettings := initialSettings
	updatedSettings.UpstreamModelUpdateLastCheckTime = 2
	require.NoError(t, updateChannelUpstreamModelSettings(
		channel,
		updatedSettings,
		false,
		channelAuditTestActor(),
		"channel_upstream_detect",
		"",
	))

	var count int64
	require.NoError(t, db.Model(&model.ChannelAuditLog{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestPersistCodexUsageCredentialRefreshAuditsWithoutCredentialLeak(t *testing.T) {
	db := setupChannelAuditWriteTestDB(t)

	const oldAccessToken = "codex-old-access-secret"
	const newAccessToken = "codex-new-access-secret"
	channel := &model.Channel{
		Type:        57,
		Key:         `{"access_token":"` + oldAccessToken + `","refresh_token":"codex-refresh-secret","account_id":"account"}`,
		Status:      common.ChannelStatusEnabled,
		Name:        "audit-codex-refresh",
		Models:      "codex-mini-latest",
		Group:       "default",
		CreatedTime: 1,
	}
	require.NoError(t, db.Create(channel).Error)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/1/codex/usage", nil)
	c.Set("id", 7)
	c.Set("username", "audit-admin")
	c.Set("role", 10)

	encoded := []byte(`{"access_token":"` + newAccessToken + `","refresh_token":"codex-new-refresh-secret","account_id":"account"}`)
	require.NoError(t, persistCodexUsageCredentialRefresh(c, channel.Id, encoded))

	var updated model.Channel
	require.NoError(t, db.First(&updated, channel.Id).Error)
	require.Equal(t, string(encoded), updated.Key)

	var audit model.ChannelAuditLog
	require.NoError(t, db.First(&audit).Error)
	require.Equal(t, model.ChannelAuditActionCredentialChange, audit.Action)
	require.Equal(t, "channel_codex_usage_credential_refresh", audit.Source)
	require.Equal(t, "audit-admin", audit.OperatorName)

	serializedAudit := string(audit.BeforeSnapshot) + string(audit.AfterSnapshot) + string(audit.Changes)
	require.NotContains(t, serializedAudit, oldAccessToken)
	require.NotContains(t, serializedAudit, newAccessToken)
	require.NotContains(t, serializedAudit, "codex-refresh-secret")
	require.NotContains(t, serializedAudit, "codex-new-refresh-secret")
}

func TestPersistCodexUsageCredentialRefreshRollsBackWhenAuditWriteFails(t *testing.T) {
	db := setupChannelAuditWriteTestDB(t)

	const originalKey = `{"access_token":"original-secret","refresh_token":"refresh-secret","account_id":"account"}`
	channel := &model.Channel{
		Type:        57,
		Key:         originalKey,
		Status:      common.ChannelStatusEnabled,
		Name:        "audit-codex-rollback",
		Models:      "codex-mini-latest",
		Group:       "default",
		CreatedTime: 1,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Migrator().DropTable(&model.ChannelAuditLog{}))

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/1/codex/usage", nil)
	c.Set("id", 7)
	c.Set("username", "audit-admin")
	c.Set("role", 10)

	err := persistCodexUsageCredentialRefresh(
		c,
		channel.Id,
		[]byte(`{"access_token":"replacement-secret","refresh_token":"new-refresh-secret","account_id":"account"}`),
	)
	require.Error(t, err)

	var persisted model.Channel
	require.NoError(t, db.First(&persisted, channel.Id).Error)
	require.Equal(t, originalKey, persisted.Key)
}
