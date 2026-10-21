package controller

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupRelayEndpointTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousSQLitePath := common.SQLitePath
	previousSQLite, previousMySQL, previousPostgreSQL := common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL
	previousMaster, previousMemoryCache := common.IsMasterNode, common.MemoryCacheEnabled
	t.Setenv("SQL_DSN", "")
	common.SQLitePath = filepath.Join(t.TempDir(), "relay-endpoint.db")
	common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = true, false, false
	common.IsMasterNode, common.MemoryCacheEnabled = false, false
	t.Cleanup(func() {
		model.DB = previousDB
		common.SQLitePath = previousSQLitePath
		common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = previousSQLite, previousMySQL, previousPostgreSQL
		common.IsMasterNode, common.MemoryCacheEnabled = previousMaster, previousMemoryCache
	})
	// InitDB also initializes the database-specific quoting used by channel selection.
	require.NoError(t, model.InitDB())
	db := model.DB
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	return db
}

func TestTokenLimitReselectionPreservesClientEndpoint(t *testing.T) {
	for _, tt := range []struct {
		name            string
		path            string
		disabledSetting string
		allowedSetting  string
	}{
		{
			name:            "messages",
			path:            "/v1/messages?beta=true",
			disabledSetting: `{"messages_enabled":false}`,
			allowedSetting:  `{"responses_enabled":false,"chat_completions_enabled":false}`,
		},
		{
			name:            "chat_completions",
			path:            "/v1/chat/completions",
			disabledSetting: `{"chat_completions_enabled":false}`,
			allowedSetting:  `{"responses_enabled":false,"messages_enabled":false}`,
		},
		{
			name:            "playground",
			path:            "/pg/chat/completions",
			disabledSetting: `{"chat_completions_enabled":false}`,
			allowedSetting:  `{"responses_enabled":false,"messages_enabled":false}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db := setupRelayEndpointTestDB(t)

			maxOutput := 5
			initial := &model.Channel{MaxOutputTokens: &maxOutput}
			disabled := &model.Channel{Setting: &tt.disabledSetting}
			allowed := &model.Channel{Setting: &tt.allowedSetting}
			for i, channel := range []*model.Channel{initial, disabled, allowed} {
				priority := int64(150 - i*50)
				channel.Type = constant.ChannelTypeOpenAI
				channel.Status = common.ChannelStatusEnabled
				channel.Key = "test-key"
				channel.Priority = &priority
				require.NoError(t, db.Create(channel).Error)
				require.NoError(t, db.Create(&model.Ability{
					Group:     "default",
					Model:     "endpoint-test-model",
					ChannelId: channel.Id,
					Enabled:   true,
					Priority:  &priority,
				}).Error)
			}

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, nil)
			common.SetContextKey(c, constant.ContextKeyChannelId, initial.Id)
			info := &relaycommon.RelayInfo{
				TokenGroup:      "default",
				OriginModelName: "endpoint-test-model",
				// Upstream conversion must not change the client endpoint restriction.
				RelayMode: relayconstant.RelayModeResponses,
			}

			require.Nil(t, ensureSelectedChannelSatisfiesTokenLimit(c, info, 10, 20))
			require.Equal(t, allowed.Id, common.GetContextKeyInt(c, constant.ContextKeyChannelId))
		})
	}
}
