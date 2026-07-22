package model

import (
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/stretchr/testify/require"
)

func setupChannelAuditTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&ChannelAuditLog{}))
	require.NoError(t, DB.Exec("DELETE FROM channel_audit_logs").Error)
	t.Cleanup(func() {
		_ = DB.Exec("DELETE FROM channel_audit_logs").Error
	})
}

func auditStringPointer(value string) *string {
	return &value
}

func auditIntPointer(value int) *int {
	return &value
}

func auditInt64Pointer(value int64) *int64 {
	return &value
}

func auditUintPointer(value uint) *uint {
	return &value
}

func TestRecordChannelAuditPairsSanitizesSecretsAndUsesRawDiff(t *testing.T) {
	setupChannelAuditTest(t)
	beforeHeader := `{"X-Custom-Auth":"header-secret-before","X-Trace":"trace-before"}`
	afterHeader := `{"X-Custom-Auth":"header-secret-after","X-Trace":"trace-after"}`
	beforeSetting := `{"proxy":"https://proxy-user:proxy-pass@example.com?token=proxy-token","system_prompt":"do not echo old-channel-key; call https://embed-user:embed-pass@example.net/run?access_token=embed-token and keep text"}`
	afterSetting := `{"proxy":"https://new-user:new-pass@example.com?token=new-proxy-token","system_prompt":"do not echo new-channel-key; call https://next-embed-user:next-embed-pass@example.net/run?access_token=next-embed-token and keep text"}`
	beforeOther := `{"access_token":"nested-token-before","key":"nested-key-before","webhook_secret":"webhook-secret-before","db_password":"db-password-before","aws_access_key":"aws-access-key-before","large":9007199254740993}`
	afterOther := `{"access_token":"nested-token-after","key":"nested-key-after","webhook_secret":"webhook-secret-after","db_password":"db-password-after","aws_access_key":"aws-access-key-after","large":9007199254740993}`
	beforeBaseURL := "https://url-user:url-pass@example.com/v1/webhook/path-secret-before-123456789?api_key=url-key-before&sig=url-signature-before&code=url-code-before&subscription-key=url-subscription-before&region=cn#fragment-secret-before"
	afterBaseURL := "https://next-user:next-pass@example.com/v1/webhook/path-secret-after-987654321?api_key=url-key-after&sig=url-signature-after&code=url-code-after&subscription-key=url-subscription-after&region=cn#fragment-secret-after"

	before := &Channel{
		Id:             7,
		Type:           1,
		Name:           "secure-channel",
		Key:            "old-channel-key",
		BaseURL:        &beforeBaseURL,
		Other:          beforeOther,
		Models:         "gpt-4o",
		Group:          "default",
		Setting:        &beforeSetting,
		HeaderOverride: &beforeHeader,
		ChannelInfo: ChannelInfo{
			MultiKeyDisabledReason: map[int]string{0: "upstream-error-secret-before"},
		},
	}
	after := &Channel{
		Id:             7,
		Type:           1,
		Name:           "secure-channel",
		Key:            "new-channel-key",
		BaseURL:        &afterBaseURL,
		Other:          afterOther,
		Models:         "gpt-4o,gpt-4.1",
		Group:          "default",
		Setting:        &afterSetting,
		HeaderOverride: &afterHeader,
		ChannelInfo: ChannelInfo{
			MultiKeyDisabledReason: map[int]string{0: "upstream-error-secret-after"},
		},
	}
	actor := ChannelAuditActor{
		Type:      ChannelAuditActorAdmin,
		ID:        11,
		Name:      "root",
		Role:      100,
		RequestID: "request-1",
		IP:        "127.0.0.1",
		UserAgent: "audit-test",
		Method:    "PUT",
		Path:      "/api/channel/",
	}
	require.NoError(t, RecordChannelAuditPairs(DB, actor, "channel_update", "batch-1", []ChannelAuditPair{{
		Before: before,
		After:  after,
	}}))

	var log ChannelAuditLog
	require.NoError(t, DB.First(&log).Error)
	require.Equal(t, ChannelAuditActionUpdate, log.Action)
	require.Equal(t, "channel_update", log.Source)
	require.Equal(t, "batch-1", log.BatchId)
	require.Equal(t, 11, log.OperatorId)

	persisted := strings.Join([]string{string(log.BeforeSnapshot), string(log.AfterSnapshot), string(log.Changes), string(log.ChangedFields)}, "\n")
	for _, secret := range []string{
		"old-channel-key", "new-channel-key", "header-secret-before", "header-secret-after",
		"trace-before", "trace-after", "proxy-user", "proxy-pass", "new-user", "new-pass",
		"proxy-token", "new-proxy-token", "nested-token-before", "nested-token-after",
		"nested-key-before", "nested-key-after", "webhook-secret-before", "webhook-secret-after",
		"db-password-before", "db-password-after", "aws-access-key-before", "aws-access-key-after",
		"url-user", "url-pass", "next-user", "next-pass", "url-key-before", "url-key-after",
		"url-signature-before", "url-signature-after", "url-code-before", "url-code-after",
		"url-subscription-before", "url-subscription-after",
		"path-secret-before-123456789", "path-secret-after-987654321",
		"fragment-secret-before", "fragment-secret-after",
		"upstream-error-secret-before", "upstream-error-secret-after",
		"embed-user", "embed-pass", "embed-token", "next-embed-user", "next-embed-pass", "next-embed-token",
	} {
		require.NotContains(t, persisted, secret)
	}
	require.Contains(t, persisted, channelAuditRedactedValue)
	require.Contains(t, string(log.BeforeSnapshot), `"large":9007199254740993`)

	var beforeSnapshot map[string]any
	require.NoError(t, common.Unmarshal(log.BeforeSnapshot, &beforeSnapshot))
	header, ok := beforeSnapshot["header_override"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, channelAuditRedactedValue, header["X-Custom-Auth"])
	require.Equal(t, channelAuditRedactedValue, header["X-Trace"])
	baseURL, ok := beforeSnapshot["base_url"].(string)
	require.True(t, ok)
	require.NotContains(t, baseURL, "@")
	require.Contains(t, baseURL, "%5BREDACTED%5D")
	setting, ok := beforeSnapshot["setting"].(map[string]any)
	require.True(t, ok)
	systemPrompt, ok := setting["system_prompt"].(string)
	require.True(t, ok)
	require.Contains(t, systemPrompt, "and keep text")
	require.Contains(t, systemPrompt, "example.net/run")

	var changes []ChannelAuditChange
	require.NoError(t, common.Unmarshal(log.Changes, &changes))
	var keyChange *ChannelAuditChange
	for index := range changes {
		if changes[index].Field == "key" {
			keyChange = &changes[index]
			break
		}
	}
	require.NotNil(t, keyChange, "same-sized key replacement must be detected from raw values")
	require.True(t, keyChange.Sensitive)
	require.Equal(t, keyChange.Before, keyChange.After, "safe key summaries may match while the raw key changed")
}

func TestChannelAuditMasksScalarHeaderOverride(t *testing.T) {
	value, sensitive := auditSanitizeValue("header_override", "scalar-header-secret", newChannelAuditRedactor())
	require.True(t, sensitive)
	require.Equal(t, channelAuditRedactedValue, value)
}

func TestChannelAuditDoesNotGloballyReplaceShortSecrets(t *testing.T) {
	redactor := newChannelAuditRedactor(map[string]any{"key": "a"})
	require.Empty(t, redactor.secrets)
	value, sensitive := auditSanitizeValue("name", "alpha", redactor)
	require.False(t, sensitive)
	require.Equal(t, "alpha", value)
	keyValue, keySensitive := auditSanitizeValue("key", "a", redactor)
	require.True(t, keySensitive)
	require.Equal(t, map[string]any{"configured": true, "key_count": 1}, keyValue)
}

func TestRecordChannelAuditPairsExcludesRuntimeOnlyChanges(t *testing.T) {
	setupChannelAuditTest(t)
	before := &Channel{
		Id:                 8,
		Name:               "runtime-only",
		Key:                "stable-key",
		Models:             "gpt-4o",
		Group:              "default",
		Balance:            10,
		BalanceUpdatedTime: 100,
		UsedQuota:          20,
		TestTime:           30,
		ResponseTime:       40,
		OtherSettings:      `{"upstream_model_update_last_check_time":100}`,
		ChannelInfo: ChannelInfo{
			MultiKeyPollingIndex: 1,
		},
	}
	after := *before
	after.Balance = 9
	after.BalanceUpdatedTime = 101
	after.UsedQuota = 21
	after.TestTime = 31
	after.ResponseTime = 41
	after.OtherSettings = `{"upstream_model_update_last_check_time":101}`
	after.ChannelInfo.MultiKeyPollingIndex = 2

	require.NoError(t, RecordChannelAuditPairs(DB, ChannelAuditActor{Type: ChannelAuditActorSystem}, "runtime", "", []ChannelAuditPair{{
		Before: before,
		After:  &after,
	}}))
	var count int64
	require.NoError(t, DB.Model(&ChannelAuditLog{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestRecordChannelAuditPairsStoresPayloadLargerThanMySQLTextLimit(t *testing.T) {
	setupChannelAuditTest(t)
	beforeSetting := `{"system_prompt":"` + strings.Repeat("a", 70*1024) + `"}`
	afterSetting := `{"system_prompt":"` + strings.Repeat("b", 70*1024) + `"}`
	before := &Channel{Id: 18, Name: "large-audit", Key: "stable-secret", Setting: &beforeSetting}
	after := *before
	after.Setting = &afterSetting

	require.NoError(t, RecordChannelAuditPairs(DB, ChannelAuditActor{Type: ChannelAuditActorAdmin, ID: 1}, "large_payload", "", []ChannelAuditPair{{
		Before: before,
		After:  &after,
	}}))
	var log ChannelAuditLog
	require.NoError(t, DB.Where("source = ?", "large_payload").First(&log).Error)
	require.Greater(t, len(log.BeforeSnapshot), 64*1024)
	require.Greater(t, len(log.AfterSnapshot), 64*1024)
	require.Greater(t, len(log.Changes), 128*1024)
}

func TestRecordChannelAuditPairsInfersCreateDeleteAndRollsBack(t *testing.T) {
	setupChannelAuditTest(t)
	channel := &Channel{Id: 9, Type: 1, Name: "created", Key: "create-secret", Models: "gpt-4o", Group: "default"}
	tx := DB.Begin()
	require.NoError(t, tx.Error)
	require.NoError(t, RecordChannelAuditPairs(tx, ChannelAuditActor{ID: 1, Name: "root"}, "create", "batch-create", []ChannelAuditPair{{After: channel}}))
	require.NoError(t, tx.Rollback().Error)

	var count int64
	require.NoError(t, DB.Model(&ChannelAuditLog{}).Count(&count).Error)
	require.Zero(t, count, "audit insert must participate in the caller transaction")

	require.NoError(t, RecordChannelAuditPairs(DB, ChannelAuditActor{ID: 1, Name: "root"}, "lifecycle", "batch-life", []ChannelAuditPair{
		{After: channel},
		{Before: channel},
	}))
	var logs []ChannelAuditLog
	require.NoError(t, DB.Order("id asc").Find(&logs).Error)
	require.Len(t, logs, 2)
	require.Equal(t, ChannelAuditActionCreate, logs[0].Action)
	require.Equal(t, []byte("null"), logs[0].BeforeSnapshot)
	require.Equal(t, ChannelAuditActionDelete, logs[1].Action)
	require.Equal(t, []byte("null"), logs[1].AfterSnapshot)
	require.Equal(t, "batch-life", logs[0].BatchId)
	require.Equal(t, "batch-life", logs[1].BatchId)
}

func TestGetChannelAuditLogsFiltersAndReturnsLightweightRows(t *testing.T) {
	setupChannelAuditTest(t)
	records := []ChannelAuditLog{
		{ChannelId: 1, ChannelName: "alpha", Action: ChannelAuditActionUpdate, Source: "manual", OperatorName: "alice", CreatedAt: 10, BeforeSnapshot: []byte(`{}`), AfterSnapshot: []byte(`{}`), Changes: []byte(`[]`), ChangedFields: []byte(`["name"]`), ChangeCount: 1, SnapshotVersion: 1},
		{ChannelId: 2, ChannelName: "beta", Action: ChannelAuditActionDelete, Source: "manual", OperatorName: "bob", CreatedAt: 20, BeforeSnapshot: []byte(`{}`), AfterSnapshot: []byte(`null`), Changes: []byte(`[]`), ChangedFields: []byte(`["status"]`), ChangeCount: 1, SnapshotVersion: 1},
		{ChannelId: 1, ChannelName: "alpha-prod", Action: ChannelAuditActionStatusChange, Source: "auto_test", OperatorName: "system", CreatedAt: 30, BeforeSnapshot: []byte(`{}`), AfterSnapshot: []byte(`{}`), Changes: []byte(`[]`), ChangedFields: []byte(`["status"]`), ChangeCount: 1, SnapshotVersion: 1},
	}
	require.NoError(t, DB.Create(&records).Error)

	logs, total, err := GetChannelAuditLogs(0, 10, ChannelAuditQuery{
		ChannelID:    1,
		ChannelName:  "alpha",
		OperatorName: "system",
		StartTime:    25,
		EndTime:      35,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, logs, 1)
	require.Equal(t, int64(30), logs[0].CreatedAt)
	require.Empty(t, logs[0].BeforeSnapshot, "list query must omit large snapshots")
	require.Equal(t, `["status"]`, string(logs[0].ChangedFields))
}

func TestChannelAuditSnapshotClassifiesEveryChannelField(t *testing.T) {
	included := map[string]struct{}{
		"Id": {}, "Type": {}, "Key": {}, "OpenAIOrganization": {}, "TestModel": {}, "Status": {},
		"Name": {}, "Weight": {}, "MaxContextTokens": {}, "MaxOutputTokens": {}, "MinInputTokens": {},
		"MaxInputTokens": {}, "CreatedTime": {}, "BaseURL": {}, "Other": {}, "Models": {}, "Group": {},
		"ModelMapping": {}, "StatusCodeMapping": {}, "Priority": {}, "AutoBan": {}, "OtherInfo": {},
		"Tag": {}, "Setting": {}, "ParamOverride": {}, "HeaderOverride": {}, "Remark": {}, "ChannelInfo": {},
		"OtherSettings": {},
	}
	excludedRuntime := map[string]struct{}{
		"TestTime": {}, "ResponseTime": {}, "Balance": {}, "BalanceUpdatedTime": {}, "UsedQuota": {}, "Keys": {},
	}
	channelType := reflect.TypeOf(Channel{})
	for index := 0; index < channelType.NumField(); index++ {
		name := channelType.Field(index).Name
		_, isIncluded := included[name]
		_, isExcluded := excludedRuntime[name]
		require.NotEqual(t, isIncluded, isExcluded, "Channel field %s must be explicitly included or excluded", name)
	}
	require.Equal(t, channelType.NumField(), len(included)+len(excludedRuntime))
}

func TestChannelAuditSnapshotPreservesConfigPointersAndNormalizesLists(t *testing.T) {
	channel := &Channel{
		Id:               1,
		Weight:           auditUintPointer(0),
		Priority:         auditInt64Pointer(0),
		AutoBan:          auditIntPointer(0),
		Models:           " gpt-4.1, gpt-4o,gpt-4.1 ",
		Group:            "vip, default,vip",
		ModelMapping:     auditStringPointer(`{"alias":"target"}`),
		OtherSettings:    `{"upstream_model_update_check_enabled":true,"upstream_model_update_last_detected_models":["new-model"]}`,
		ChannelInfo:      ChannelInfo{MultiKeyMode: constant.MultiKeyModeRandom},
		MaxContextTokens: auditIntPointer(0),
	}
	snapshot := channelAuditRawSnapshot(channel)
	require.Equal(t, uint(0), snapshot["weight"])
	require.Equal(t, int64(0), snapshot["priority"])
	require.Equal(t, []string{"gpt-4.1", "gpt-4o"}, snapshot["models"])
	require.Equal(t, []string{"default", "vip"}, snapshot["group"])
	settings := snapshot["settings"].(map[string]any)
	require.Contains(t, settings, "upstream_model_update_last_detected_models")
}
