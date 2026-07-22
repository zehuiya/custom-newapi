package model

import (
	"errors"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelAuditMutationTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&Ability{}, &ChannelAuditLog{}))
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channel_audit_logs").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)
	t.Cleanup(func() {
		_ = DB.AutoMigrate(&ChannelAuditLog{})
		_ = DB.Exec("DELETE FROM abilities").Error
		_ = DB.Exec("DELETE FROM channel_audit_logs").Error
		_ = DB.Exec("DELETE FROM channels").Error
	})
}

func newAuditedMutationChannel(name string, key string) Channel {
	priority := int64(0)
	weight := uint(0)
	autoBan := 1
	return Channel{
		Type:        1,
		Key:         key,
		Status:      common.ChannelStatusEnabled,
		Name:        name,
		Weight:      &weight,
		CreatedTime: common.GetTimestamp(),
		Models:      "audit-model",
		Group:       "default",
		Priority:    &priority,
		AutoBan:     &autoBan,
	}
}

func TestChannelUpdateAndDeleteWithAuditUseActualDatabaseSnapshots(t *testing.T) {
	setupChannelAuditMutationTest(t)
	channel := newAuditedMutationChannel("before-name", "before-secret-key")
	require.NoError(t, DB.Create(&channel).Error)
	require.NoError(t, channel.AddAbilities(nil))

	channel.Name = "after-name"
	channel.Key = "after-secret-key"
	actor := ChannelAuditActor{Type: ChannelAuditActorAdmin, ID: 1, Name: "root"}
	require.NoError(t, channel.UpdateWithAudit(actor, "channel_update", ""))

	var updateLog ChannelAuditLog
	require.NoError(t, DB.Where("source = ?", "channel_update").First(&updateLog).Error)
	require.Equal(t, ChannelAuditActionUpdate, updateLog.Action)
	require.Contains(t, string(updateLog.BeforeSnapshot), "before-name")
	require.Contains(t, string(updateLog.AfterSnapshot), "after-name")
	persistedUpdate := strings.Join([]string{string(updateLog.BeforeSnapshot), string(updateLog.AfterSnapshot), string(updateLog.Changes)}, "\n")
	require.NotContains(t, persistedUpdate, "before-secret-key")
	require.NotContains(t, persistedUpdate, "after-secret-key")

	require.NoError(t, (&Channel{Id: channel.Id}).DeleteWithAudit(actor, "channel_delete", ""))
	var deleteLog ChannelAuditLog
	require.NoError(t, DB.Where("source = ?", "channel_delete").First(&deleteLog).Error)
	require.Equal(t, ChannelAuditActionDelete, deleteLog.Action)
	require.Contains(t, string(deleteLog.BeforeSnapshot), "after-name")
	require.Equal(t, []byte("null"), deleteLog.AfterSnapshot)

	var deleted Channel
	err := DB.First(&deleted, "id = ?", channel.Id).Error
	require.True(t, errors.Is(err, gorm.ErrRecordNotFound))
	var abilityCount int64
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", channel.Id).Count(&abilityCount).Error)
	require.Zero(t, abilityCount)
}

func TestChannelUpdateWithAuditRollsBackWhenAuditCannotPersist(t *testing.T) {
	setupChannelAuditMutationTest(t)
	channel := newAuditedMutationChannel("stable-name", "stable-secret-key")
	require.NoError(t, DB.Create(&channel).Error)
	require.NoError(t, channel.AddAbilities(nil))
	require.NoError(t, DB.Migrator().DropTable(&ChannelAuditLog{}))

	channel.Name = "must-rollback"
	err := channel.UpdateWithAudit(ChannelAuditActor{ID: 1, Name: "root"}, "channel_update", "")
	require.Error(t, err)

	var persisted Channel
	require.NoError(t, DB.First(&persisted, "id = ?", channel.Id).Error)
	require.Equal(t, "stable-name", persisted.Name)
	require.NoError(t, DB.AutoMigrate(&ChannelAuditLog{}))
}

func TestBatchChannelMutationsShareBatchAndAuditEveryChannel(t *testing.T) {
	setupChannelAuditMutationTest(t)
	channels := []Channel{
		newAuditedMutationChannel("batch-one", "batch-secret-one"),
		newAuditedMutationChannel("batch-two", "batch-secret-two"),
	}
	actor := ChannelAuditActor{Type: ChannelAuditActorAdmin, ID: 1, Name: "root"}
	require.NoError(t, BatchInsertChannelsWithAudit(channels, actor, "channel_create", "batch-create"))
	require.NotZero(t, channels[0].Id)
	require.NotZero(t, channels[1].Id)

	var createLogs []ChannelAuditLog
	require.NoError(t, DB.Where("batch_id = ?", "batch-create").Order("id asc").Find(&createLogs).Error)
	require.Len(t, createLogs, 2)
	for _, log := range createLogs {
		require.Equal(t, ChannelAuditActionCreate, log.Action)
		require.Equal(t, "batch-create", log.BatchId)
		require.NotContains(t, string(log.AfterSnapshot), "batch-secret")
	}

	ids := []int{channels[0].Id, channels[1].Id}
	require.NoError(t, BatchDeleteChannelsWithAudit(ids, actor, "channel_batch_delete", "batch-delete"))
	var deleteLogs []ChannelAuditLog
	require.NoError(t, DB.Where("batch_id = ?", "batch-delete").Find(&deleteLogs).Error)
	require.Len(t, deleteLogs, 2)
	for _, log := range deleteLogs {
		require.Equal(t, ChannelAuditActionDelete, log.Action)
	}
}

func TestChannelSpecializedActionsAndStatusDeletionAreAudited(t *testing.T) {
	setupChannelAuditMutationTest(t)
	tag := "audit-tag"
	channel := newAuditedMutationChannel("special-actions", "special-secret-key")
	channel.Tag = &tag
	require.NoError(t, DB.Create(&channel).Error)
	require.NoError(t, channel.AddAbilities(nil))

	actor := ChannelAuditActor{Type: ChannelAuditActorAdmin, ID: 1, Name: "root"}
	require.NoError(t, UpdateChannelStatusByTagWithAudit(
		tag,
		common.ChannelStatusManuallyDisabled,
		actor,
		"test_status_change",
		"",
	))
	var statusLog ChannelAuditLog
	require.NoError(t, DB.Where("source = ?", "test_status_change").First(&statusLog).Error)
	require.Equal(t, ChannelAuditActionStatusChange, statusLog.Action)

	newTag := "audit-tag-updated"
	require.NoError(t, BatchSetChannelTagWithAudit(
		[]int{channel.Id},
		&newTag,
		actor,
		"test_tag_change",
		"",
	))
	var tagLog ChannelAuditLog
	require.NoError(t, DB.Where("source = ?", "test_tag_change").First(&tagLog).Error)
	require.Equal(t, ChannelAuditActionTagUpdate, tagLog.Action)

	rows, err := DeleteChannelByStatus(int64(common.ChannelStatusManuallyDisabled))
	require.NoError(t, err)
	require.EqualValues(t, 1, rows)
	var deleteLog ChannelAuditLog
	require.NoError(t, DB.Where("source = ?", "channel_delete_by_status_internal").First(&deleteLog).Error)
	require.Equal(t, ChannelAuditActionDelete, deleteLog.Action)
	require.Equal(t, ChannelAuditActorSystem, deleteLog.OperatorType)
}

func TestSaveChannelInfoOnlyPersistsPollingIndex(t *testing.T) {
	setupChannelAuditMutationTest(t)
	channel := newAuditedMutationChannel("polling-runtime", "polling-secret-key")
	channel.ChannelInfo = ChannelInfo{
		IsMultiKey:         true,
		MultiKeySize:       2,
		MultiKeyStatusList: map[int]int{0: common.ChannelStatusEnabled, 1: common.ChannelStatusManuallyDisabled},
		MultiKeyMode:       constant.MultiKeyModeRandom,
	}
	require.NoError(t, DB.Create(&channel).Error)

	staleRelayCopy := &Channel{
		Id: channel.Id,
		ChannelInfo: ChannelInfo{
			MultiKeyPollingIndex: 1,
		},
	}
	require.NoError(t, staleRelayCopy.SaveChannelInfo())

	var persisted Channel
	require.NoError(t, DB.First(&persisted, "id = ?", channel.Id).Error)
	require.True(t, persisted.ChannelInfo.IsMultiKey)
	require.Equal(t, 2, persisted.ChannelInfo.MultiKeySize)
	require.Equal(t, constant.MultiKeyModeRandom, persisted.ChannelInfo.MultiKeyMode)
	require.Equal(t, common.ChannelStatusManuallyDisabled, persisted.ChannelInfo.MultiKeyStatusList[1])
	require.Equal(t, 1, persisted.ChannelInfo.MultiKeyPollingIndex)
	require.Equal(t, persisted.ChannelInfo, staleRelayCopy.ChannelInfo)

	var auditCount int64
	require.NoError(t, DB.Model(&ChannelAuditLog{}).Count(&auditCount).Error)
	require.Zero(t, auditCount, "polling index is runtime state and must not create audit noise")
}
