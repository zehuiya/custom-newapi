package controller

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

type channelAuditListItem struct {
	Id              int64    `json:"id"`
	ChannelId       int      `json:"channel_id"`
	ChannelName     string   `json:"channel_name"`
	ChannelType     int      `json:"channel_type"`
	Action          string   `json:"action"`
	Source          string   `json:"source"`
	OperatorType    string   `json:"operator_type"`
	OperatorId      int      `json:"operator_id"`
	OperatorName    string   `json:"operator_name"`
	OperatorRole    int      `json:"operator_role"`
	RequestId       string   `json:"request_id"`
	Ip              string   `json:"ip"`
	UserAgent       string   `json:"user_agent"`
	Method          string   `json:"method"`
	Path            string   `json:"path"`
	BatchId         string   `json:"batch_id"`
	CreatedAt       int64    `json:"created_at"`
	ChangeCount     int      `json:"change_count"`
	ChangedFields   []string `json:"changed_fields"`
	SnapshotVersion int      `json:"snapshot_version"`
}

type channelAuditDetail struct {
	channelAuditListItem
	Before  json.RawMessage `json:"before_snapshot"`
	After   json.RawMessage `json:"after_snapshot"`
	Changes json.RawMessage `json:"changes"`
}

// NewChannelAuditActor captures request metadata before mutation code enters a
// transaction. Background tasks should construct model.ChannelAuditActor with
// Type=model.ChannelAuditActorSystem explicitly instead.
func NewChannelAuditActor(c *gin.Context) model.ChannelAuditActor {
	actor := model.ChannelAuditActor{
		Type:      model.ChannelAuditActorAdmin,
		ID:        c.GetInt("id"),
		Name:      c.GetString("username"),
		Role:      c.GetInt("role"),
		RequestID: c.GetString(common.RequestIdKey),
		IP:        c.ClientIP(),
	}
	if c.Request != nil {
		actor.UserAgent = c.Request.UserAgent()
		actor.Method = c.Request.Method
		if c.Request.URL != nil {
			// URL.Path intentionally excludes query credentials.
			actor.Path = c.Request.URL.Path
		}
	}
	return actor
}

func parseChannelAuditListItem(log *model.ChannelAuditLog) (channelAuditListItem, error) {
	changedFields := make([]string, 0)
	if len(log.ChangedFields) > 0 {
		if err := common.Unmarshal(log.ChangedFields, &changedFields); err != nil {
			return channelAuditListItem{}, fmt.Errorf("parse channel audit changed fields: %w", err)
		}
	}
	return channelAuditListItem{
		Id:              log.Id,
		ChannelId:       log.ChannelId,
		ChannelName:     log.ChannelName,
		ChannelType:     log.ChannelType,
		Action:          log.Action,
		Source:          log.Source,
		OperatorType:    log.OperatorType,
		OperatorId:      log.OperatorId,
		OperatorName:    log.OperatorName,
		OperatorRole:    log.OperatorRole,
		RequestId:       log.RequestId,
		Ip:              log.Ip,
		UserAgent:       log.UserAgent,
		Method:          log.Method,
		Path:            log.Path,
		BatchId:         log.BatchId,
		CreatedAt:       log.CreatedAt,
		ChangeCount:     log.ChangeCount,
		ChangedFields:   changedFields,
		SnapshotVersion: log.SnapshotVersion,
	}, nil
}

func parseChannelAuditJSON(raw []byte, fallback string) (json.RawMessage, error) {
	if len(raw) == 0 {
		raw = []byte(fallback)
	}
	var message json.RawMessage
	if err := common.Unmarshal(raw, &message); err != nil {
		return nil, err
	}
	return message, nil
}

func GetChannelAuditLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	if pageInfo.Page < 1 {
		pageInfo.Page = 1
	}
	if pageInfo.PageSize < 1 {
		pageInfo.PageSize = common.ItemsPerPage
	}
	channelID, _ := strconv.Atoi(c.Query("channel_id"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	operatorName := strings.TrimSpace(c.Query("operator_name"))
	if operatorName == "" {
		operatorName = strings.TrimSpace(c.Query("operator"))
	}
	query := model.ChannelAuditQuery{
		ChannelID:    channelID,
		ChannelName:  strings.TrimSpace(c.Query("channel_name")),
		Action:       strings.TrimSpace(c.Query("action")),
		Source:       strings.TrimSpace(c.Query("source")),
		OperatorName: operatorName,
		StartTime:    startTimestamp,
		EndTime:      endTimestamp,
	}

	logs, total, err := model.GetChannelAuditLogs(pageInfo.GetStartIdx(), pageInfo.GetPageSize(), query)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items := make([]channelAuditListItem, 0, len(logs))
	for _, log := range logs {
		item, err := parseChannelAuditListItem(log)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		items = append(items, item)
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

func GetChannelAuditLog(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiError(c, fmt.Errorf("invalid channel audit id"))
		return
	}
	log, err := model.GetChannelAuditLogByID(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	item, err := parseChannelAuditListItem(log)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	before, err := parseChannelAuditJSON(log.BeforeSnapshot, "null")
	if err != nil {
		common.ApiError(c, fmt.Errorf("parse channel audit before snapshot: %w", err))
		return
	}
	after, err := parseChannelAuditJSON(log.AfterSnapshot, "null")
	if err != nil {
		common.ApiError(c, fmt.Errorf("parse channel audit after snapshot: %w", err))
		return
	}
	changes, err := parseChannelAuditJSON(log.Changes, "[]")
	if err != nil {
		common.ApiError(c, fmt.Errorf("parse channel audit changes: %w", err))
		return
	}
	common.ApiSuccess(c, channelAuditDetail{
		channelAuditListItem: item,
		Before:               before,
		After:                after,
		Changes:              changes,
	})
}
