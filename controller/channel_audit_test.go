package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type channelAuditAPIResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type channelAuditPageResponse struct {
	Page     int                    `json:"page"`
	PageSize int                    `json:"page_size"`
	Total    int                    `json:"total"`
	Items    []channelAuditListItem `json:"items"`
}

func setupChannelAuditControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousUsingSQLite := common.UsingSQLite
	previousUsingMySQL := common.UsingMySQL
	previousUsingPostgreSQL := common.UsingPostgreSQL
	gin.SetMode(gin.TestMode)
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ChannelAuditLog{}))
	model.DB = db
	model.LOG_DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.UsingSQLite = previousUsingSQLite
		common.UsingMySQL = previousUsingMySQL
		common.UsingPostgreSQL = previousUsingPostgreSQL
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func newChannelAuditTestContext(method string, target string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(method, target, nil)
	return context, recorder
}

func TestNewChannelAuditActorCapturesRequestWithoutQuery(t *testing.T) {
	context, _ := newChannelAuditTestContext(http.MethodPut, "/api/channel/?api_key=secret-query-value")
	context.Request.RemoteAddr = "127.0.0.1:54321"
	context.Request.Header.Set("User-Agent", "channel-audit-test")
	context.Set("id", 12)
	context.Set("username", "admin-user")
	context.Set("role", 10)
	context.Set(common.RequestIdKey, "request-id")

	actor := NewChannelAuditActor(context)
	require.Equal(t, model.ChannelAuditActorAdmin, actor.Type)
	require.Equal(t, 12, actor.ID)
	require.Equal(t, "admin-user", actor.Name)
	require.Equal(t, 10, actor.Role)
	require.Equal(t, "request-id", actor.RequestID)
	require.Equal(t, "127.0.0.1", actor.IP)
	require.Equal(t, http.MethodPut, actor.Method)
	require.Equal(t, "/api/channel/", actor.Path)
	require.NotContains(t, actor.Path, "secret-query-value")
}

func TestGetChannelAuditLogsReturnsPageAndParsedChangedFields(t *testing.T) {
	db := setupChannelAuditControllerTestDB(t)
	records := []model.ChannelAuditLog{
		{ChannelId: 1, ChannelName: "alpha", ChannelType: 1, Action: model.ChannelAuditActionUpdate, Source: "manual", OperatorName: "alice", CreatedAt: 10, ChangedFields: []byte(`["name"]`), ChangeCount: 1, SnapshotVersion: 1},
		{ChannelId: 2, ChannelName: "beta", ChannelType: 2, Action: model.ChannelAuditActionDelete, Source: "manual", OperatorName: "bob", CreatedAt: 20, ChangedFields: []byte(`["status","models"]`), ChangeCount: 2, SnapshotVersion: 1},
	}
	require.NoError(t, db.Create(&records).Error)

	context, recorder := newChannelAuditTestContext(http.MethodGet, "/api/channel_audit/?p=1&page_size=10&channel_id=2&operator_name=bob&start_timestamp=15&end_timestamp=25")
	GetChannelAuditLogs(context)
	require.Equal(t, http.StatusOK, recorder.Code)

	var response channelAuditAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success, response.Message)
	var page channelAuditPageResponse
	require.NoError(t, common.Unmarshal(response.Data, &page))
	require.Equal(t, 1, page.Total)
	require.Len(t, page.Items, 1)
	require.Equal(t, 2, page.Items[0].ChannelId)
	require.Equal(t, []string{"status", "models"}, page.Items[0].ChangedFields)
	require.Equal(t, 2, page.Items[0].ChangeCount)
	require.NotContains(t, recorder.Body.String(), "before_snapshot")
}

func TestGetChannelAuditLogsClampsNegativePagination(t *testing.T) {
	db := setupChannelAuditControllerTestDB(t)
	records := []model.ChannelAuditLog{
		{ChannelId: 1, ChannelName: "alpha", Action: model.ChannelAuditActionUpdate, CreatedAt: 10, ChangedFields: []byte(`["name"]`), ChangeCount: 1},
		{ChannelId: 2, ChannelName: "beta", Action: model.ChannelAuditActionDelete, CreatedAt: 20, ChangedFields: []byte(`["status"]`), ChangeCount: 1},
	}
	require.NoError(t, db.Create(&records).Error)

	context, recorder := newChannelAuditTestContext(http.MethodGet, "/api/channel_audit/?p=-1&page_size=-1")
	GetChannelAuditLogs(context)
	require.Equal(t, http.StatusOK, recorder.Code)

	var response channelAuditAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success, response.Message)
	var page channelAuditPageResponse
	require.NoError(t, common.Unmarshal(response.Data, &page))
	require.Equal(t, 1, page.Page)
	require.Equal(t, common.ItemsPerPage, page.PageSize)
	require.Equal(t, 2, page.Total)
	require.Len(t, page.Items, 2)
}

func TestGetChannelAuditLogReturnsParsedJSONWithoutNumberLoss(t *testing.T) {
	db := setupChannelAuditControllerTestDB(t)
	log := model.ChannelAuditLog{
		ChannelId:       3,
		ChannelName:     "detail",
		Action:          model.ChannelAuditActionUpdate,
		CreatedAt:       30,
		BeforeSnapshot:  []byte(`{"large":9007199254740993}`),
		AfterSnapshot:   []byte(`{"large":9007199254740994}`),
		Changes:         []byte(`[{"field":"other.large","before":9007199254740993,"after":9007199254740994,"sensitive":false}]`),
		ChangedFields:   []byte(`["other.large"]`),
		ChangeCount:     1,
		SnapshotVersion: 1,
	}
	require.NoError(t, db.Create(&log).Error)

	context, recorder := newChannelAuditTestContext(http.MethodGet, fmt.Sprintf("/api/channel_audit/%d", log.Id))
	context.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", log.Id)}}
	GetChannelAuditLog(context)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"before_snapshot":{"large":9007199254740993}`)
	require.Contains(t, recorder.Body.String(), `"after_snapshot":{"large":9007199254740994}`)
	require.Contains(t, recorder.Body.String(), `"changes":[{"field":"other.large","before":9007199254740993`)

	var response channelAuditAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success, response.Message)
}
