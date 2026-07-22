package model

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"

	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Channel struct {
	Id                 int     `json:"id"`
	Type               int     `json:"type" gorm:"default:0"`
	Key                string  `json:"key" gorm:"not null"`
	OpenAIOrganization *string `json:"openai_organization"`
	TestModel          *string `json:"test_model"`
	Status             int     `json:"status" gorm:"default:1"`
	Name               string  `json:"name" gorm:"index"`
	Weight             *uint   `json:"weight" gorm:"default:0"`
	MaxContextTokens   *int    `json:"max_context_tokens" gorm:"default:0"`
	MaxOutputTokens    *int    `json:"max_output_tokens" gorm:"default:0"`
	MinInputTokens     *int    `json:"min_input_tokens" gorm:"default:0"`
	MaxInputTokens     *int    `json:"max_input_tokens" gorm:"default:0"`
	CreatedTime        int64   `json:"created_time" gorm:"bigint"`
	TestTime           int64   `json:"test_time" gorm:"bigint"`
	ResponseTime       int     `json:"response_time"` // in milliseconds
	BaseURL            *string `json:"base_url" gorm:"column:base_url;default:''"`
	Other              string  `json:"other"`
	Balance            float64 `json:"balance"` // in USD
	BalanceUpdatedTime int64   `json:"balance_updated_time" gorm:"bigint"`
	Models             string  `json:"models"`
	Group              string  `json:"group" gorm:"type:varchar(64);default:'default'"`
	UsedQuota          int64   `json:"used_quota" gorm:"bigint;default:0"`
	ModelMapping       *string `json:"model_mapping" gorm:"type:text"`
	StatusCodeMapping  *string `json:"status_code_mapping" gorm:"type:varchar(1024);default:''"`
	Priority           *int64  `json:"priority" gorm:"bigint;default:0"`
	AutoBan            *int    `json:"auto_ban" gorm:"default:1"`
	OtherInfo          string  `json:"other_info"`
	Tag                *string `json:"tag" gorm:"index"`
	Setting            *string `json:"setting" gorm:"type:text"` // 渠道额外设置
	ParamOverride      *string `json:"param_override" gorm:"type:text"`
	HeaderOverride     *string `json:"header_override" gorm:"type:text"`
	Remark             *string `json:"remark" gorm:"type:varchar(255)" validate:"max=255"`
	// add after v0.8.5
	ChannelInfo ChannelInfo `json:"channel_info" gorm:"type:json"`

	OtherSettings string `json:"settings" gorm:"column:settings"` // 其他设置，存储azure版本等不需要检索的信息，详见dto.ChannelOtherSettings

	// cache info
	Keys []string `json:"-" gorm:"-"`
}

type ChannelInfo struct {
	IsMultiKey             bool                  `json:"is_multi_key"`                        // 是否多Key模式
	MultiKeySize           int                   `json:"multi_key_size"`                      // 多Key模式下的Key数量
	MultiKeyStatusList     map[int]int           `json:"multi_key_status_list"`               // key状态列表，key index -> status
	MultiKeyDisabledReason map[int]string        `json:"multi_key_disabled_reason,omitempty"` // key禁用原因列表，key index -> reason
	MultiKeyDisabledTime   map[int]int64         `json:"multi_key_disabled_time,omitempty"`   // key禁用时间列表，key index -> time
	MultiKeyPollingIndex   int                   `json:"multi_key_polling_index"`             // 多Key模式下轮询的key索引
	MultiKeyMode           constant.MultiKeyMode `json:"multi_key_mode"`
}

// Value implements driver.Valuer interface
func (c ChannelInfo) Value() (driver.Value, error) {
	return common.Marshal(&c)
}

// Scan implements sql.Scanner interface
func (c *ChannelInfo) Scan(value interface{}) error {
	bytesValue, _ := value.([]byte)
	return common.Unmarshal(bytesValue, c)
}

func (channel *Channel) GetKeys() []string {
	if channel.Key == "" {
		return []string{}
	}
	if len(channel.Keys) > 0 {
		return channel.Keys
	}
	trimmed := strings.TrimSpace(channel.Key)
	// If the key starts with '[', try to parse it as a JSON array (e.g., for Vertex AI scenarios)
	if strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if err := common.Unmarshal([]byte(trimmed), &arr); err == nil {
			res := make([]string, len(arr))
			for i, v := range arr {
				res[i] = string(v)
			}
			return res
		}
	}
	// Otherwise, fall back to splitting by newline
	keys := strings.Split(strings.Trim(channel.Key, "\n"), "\n")
	return keys
}

func (channel *Channel) GetNextEnabledKey() (string, int, *types.NewAPIError) {
	// If not in multi-key mode, return the original key string directly.
	if !channel.ChannelInfo.IsMultiKey {
		return channel.Key, 0, nil
	}

	// Obtain all keys (split by \n)
	keys := channel.GetKeys()
	if len(keys) == 0 {
		// No keys available, return error, should disable the channel
		return "", 0, types.NewError(errors.New("no keys available"), types.ErrorCodeChannelNoAvailableKey)
	}

	lock := GetChannelPollingLock(channel.Id)
	lock.Lock()
	defer lock.Unlock()

	statusList := channel.ChannelInfo.MultiKeyStatusList
	// helper to get key status, default to enabled when missing
	getStatus := func(idx int) int {
		if statusList == nil {
			return common.ChannelStatusEnabled
		}
		if status, ok := statusList[idx]; ok {
			return status
		}
		return common.ChannelStatusEnabled
	}

	// Collect indexes of enabled keys
	enabledIdx := make([]int, 0, len(keys))
	for i := range keys {
		if getStatus(i) == common.ChannelStatusEnabled {
			enabledIdx = append(enabledIdx, i)
		}
	}
	// If no specific status list or none enabled, return an explicit error so caller can
	// properly handle a channel with no available keys (e.g. mark channel disabled).
	// Returning the first key here caused requests to keep using an already-disabled key.
	if len(enabledIdx) == 0 {
		return "", 0, types.NewError(errors.New("no enabled keys"), types.ErrorCodeChannelNoAvailableKey)
	}

	switch channel.ChannelInfo.MultiKeyMode {
	case constant.MultiKeyModeRandom:
		// Randomly pick one enabled key
		selectedIdx := enabledIdx[rand.Intn(len(enabledIdx))]
		return keys[selectedIdx], selectedIdx, nil
	case constant.MultiKeyModePolling:
		// Use channel-specific lock to ensure thread-safe polling

		channelInfo, err := CacheGetChannelInfo(channel.Id)
		if err != nil {
			return "", 0, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
		}
		//println("before polling index:", channel.ChannelInfo.MultiKeyPollingIndex)
		defer func() {
			if common.DebugEnabled {
				println(fmt.Sprintf("channel %d polling index: %d", channel.Id, channel.ChannelInfo.MultiKeyPollingIndex))
			}
			if !common.MemoryCacheEnabled {
				_ = channel.SaveChannelInfo()
			} else {
				// CacheUpdateChannel(channel)
			}
		}()
		// Start from the saved polling index and look for the next enabled key
		start := channelInfo.MultiKeyPollingIndex
		if start < 0 || start >= len(keys) {
			start = 0
		}
		for i := 0; i < len(keys); i++ {
			idx := (start + i) % len(keys)
			if getStatus(idx) == common.ChannelStatusEnabled {
				// update polling index for next call (point to the next position)
				channel.ChannelInfo.MultiKeyPollingIndex = (idx + 1) % len(keys)
				return keys[idx], idx, nil
			}
		}
		// Fallback – should not happen, but return first enabled key
		return keys[enabledIdx[0]], enabledIdx[0], nil
	default:
		// Unknown mode, default to first enabled key (or original key string)
		return keys[enabledIdx[0]], enabledIdx[0], nil
	}
}

func (channel *Channel) SaveChannelInfo() error {
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	query := WithChannelMutationLock(tx).Select("id", "channel_info")
	persisted := &Channel{}
	if err := query.First(persisted, "id = ?", channel.Id).Error; err != nil {
		tx.Rollback()
		return err
	}
	// Polling position is runtime state. Re-read the current JSON value and
	// replace only that position so a stale relay object cannot overwrite a
	// concurrently updated multi-key mode or key status configuration.
	persisted.ChannelInfo.MultiKeyPollingIndex = channel.ChannelInfo.MultiKeyPollingIndex
	if err := tx.Model(&Channel{}).Where("id = ?", channel.Id).Update("channel_info", persisted.ChannelInfo).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	channel.ChannelInfo = persisted.ChannelInfo
	return nil
}

func (channel *Channel) GetModels() []string {
	if channel.Models == "" {
		return []string{}
	}
	return strings.Split(strings.Trim(channel.Models, ","), ",")
}

func (channel *Channel) GetGroups() []string {
	if channel.Group == "" {
		return []string{}
	}
	groups := strings.Split(strings.Trim(channel.Group, ","), ",")
	for i, group := range groups {
		groups[i] = strings.TrimSpace(group)
	}
	return groups
}

func (channel *Channel) GetOtherInfo() map[string]interface{} {
	otherInfo := make(map[string]interface{})
	if channel.OtherInfo != "" {
		err := common.Unmarshal([]byte(channel.OtherInfo), &otherInfo)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal other info: channel_id=%d, tag=%s, name=%s, error=%v", channel.Id, channel.GetTag(), channel.Name, err))
		}
	}
	return otherInfo
}

func (channel *Channel) SetOtherInfo(otherInfo map[string]interface{}) {
	otherInfoBytes, err := json.Marshal(otherInfo)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal other info: channel_id=%d, tag=%s, name=%s, error=%v", channel.Id, channel.GetTag(), channel.Name, err))
		return
	}
	channel.OtherInfo = string(otherInfoBytes)
}

func (channel *Channel) GetTag() string {
	if channel.Tag == nil {
		return ""
	}
	return *channel.Tag
}

func (channel *Channel) SetTag(tag string) {
	channel.Tag = &tag
}

func (channel *Channel) GetAutoBan() bool {
	if channel.AutoBan == nil {
		return false
	}
	return *channel.AutoBan == 1
}

func (channel *Channel) Save() error {
	return channel.SaveWithAudit(systemChannelAuditActor(), "channel_save_internal", "")
}

func (channel *Channel) SaveWithAudit(actor ChannelAuditActor, source string, batchID string) error {
	return channel.saveWithAudit(actor, source, batchID, ChannelAuditActionUpdate)
}

func (channel *Channel) saveWithAudit(actor ChannelAuditActor, source string, batchID string, action string) error {
	if channel.Id == 0 {
		return errors.New("channel ID is 0")
	}
	if err := channel.ValidateSettings(); err != nil {
		return err
	}
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()
	before := &Channel{}
	if err := WithChannelMutationLock(tx).First(before, "id = ?", channel.Id).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Save(channel).Error; err != nil {
		tx.Rollback()
		return err
	}
	after := &Channel{}
	if err := tx.First(after, "id = ?", channel.Id).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := RecordChannelAuditPairs(tx, actor, source, batchID, []ChannelAuditPair{{Before: before, After: after, Action: action}}); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	*channel = *after
	return nil
}

func (channel *Channel) SaveWithoutKey() error {
	return channel.SaveWithoutKeyWithAudit(systemChannelAuditActor(), "channel_save_internal", "")
}

func systemChannelAuditActor() ChannelAuditActor {
	return ChannelAuditActor{Type: ChannelAuditActorSystem, Name: "system"}
}

// WithChannelMutationLock serializes before/after snapshots with the actual
// row mutation on databases that support SELECT ... FOR UPDATE. SQLite has no
// row-level locking and already serializes writers at the database level.
func WithChannelMutationLock(tx *gorm.DB) *gorm.DB {
	if tx == nil || common.UsingSQLite {
		return tx
	}
	return tx.Clauses(clause.Locking{Strength: "UPDATE"})
}

func (channel *Channel) SaveWithoutKeyWithAudit(actor ChannelAuditActor, source string, batchID string) error {
	return channel.saveWithoutKeyWithAudit(actor, source, batchID, ChannelAuditActionUpdate)
}

func (channel *Channel) saveWithoutKeyWithAudit(actor ChannelAuditActor, source string, batchID string, action string) error {
	if channel.Id == 0 {
		return errors.New("channel ID is 0")
	}
	if err := channel.ValidateSettings(); err != nil {
		return err
	}
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()
	before := &Channel{}
	if err := WithChannelMutationLock(tx).First(before, "id = ?", channel.Id).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Omit("key").Save(channel).Error; err != nil {
		tx.Rollback()
		return err
	}
	after := &Channel{}
	if err := tx.First(after, "id = ?", channel.Id).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := RecordChannelAuditPairs(tx, actor, source, batchID, []ChannelAuditPair{{Before: before, After: after, Action: action}}); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	*channel = *after
	return nil
}

func GetAllChannels(startIdx int, num int, selectAll bool, idSort bool) ([]*Channel, error) {
	var channels []*Channel
	var err error
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	if selectAll {
		err = DB.Order(order).Find(&channels).Error
	} else {
		err = DB.Order(order).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	}
	return channels, err
}

func GetChannelsByTag(tag string, idSort bool, selectAll bool) ([]*Channel, error) {
	var channels []*Channel
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	query := DB.Where("tag = ?", tag).Order(order)
	if !selectAll {
		query = query.Omit("key")
	}
	err := query.Find(&channels).Error
	return channels, err
}

func SearchChannels(keyword string, group string, model string, idSort bool) ([]*Channel, error) {
	var channels []*Channel
	modelsCol := "`models`"

	// 如果是 PostgreSQL，使用双引号
	if common.UsingPostgreSQL {
		modelsCol = `"models"`
	}

	baseURLCol := "`base_url`"
	// 如果是 PostgreSQL，使用双引号
	if common.UsingPostgreSQL {
		baseURLCol = `"base_url"`
	}

	order := "priority desc"
	if idSort {
		order = "id desc"
	}

	// 构造基础查询
	baseQuery := DB.Model(&Channel{}).Omit("key")

	// 构造WHERE子句
	var whereClause string
	var args []interface{}
	if group != "" && group != "null" {
		var groupCondition string
		if common.UsingMySQL {
			groupCondition = `CONCAT(',', ` + commonGroupCol + `, ',') LIKE ?`
		} else {
			// sqlite, PostgreSQL
			groupCondition = `(',' || ` + commonGroupCol + ` || ',') LIKE ?`
		}
		whereClause = "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + ` LIKE ? AND ` + groupCondition
		args = append(args, common.String2Int(keyword), "%"+keyword+"%", keyword, "%"+keyword+"%", "%"+model+"%", "%,"+group+",%")
	} else {
		whereClause = "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
		args = append(args, common.String2Int(keyword), "%"+keyword+"%", keyword, "%"+keyword+"%", "%"+model+"%")
	}

	// 执行查询
	err := baseQuery.Where(whereClause, args...).Order(order).Find(&channels).Error
	if err != nil {
		return nil, err
	}
	return channels, nil
}

func GetChannelById(id int, selectAll bool) (*Channel, error) {
	channel := &Channel{Id: id}
	var err error = nil
	if selectAll {
		err = DB.First(channel, "id = ?", id).Error
	} else {
		err = DB.Omit("key").First(channel, "id = ?", id).Error
	}
	if err != nil {
		return nil, err
	}
	if channel == nil {
		return nil, errors.New("channel not found")
	}
	return channel, nil
}

func BatchInsertChannels(channels []Channel) error {
	return batchInsertChannels(channels, true, systemChannelAuditActor(), "channel_create_internal", common.GetUUID())
}

func BatchInsertChannelsWithAudit(channels []Channel, actor ChannelAuditActor, source string, batchID string) error {
	return batchInsertChannels(channels, true, actor, source, batchID)
}

func batchInsertChannels(channels []Channel, audit bool, actor ChannelAuditActor, source string, batchID string) error {
	if len(channels) == 0 {
		return nil
	}
	for i := range channels {
		if err := channels[i].ValidateSettings(); err != nil {
			return err
		}
	}
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	for start := 0; start < len(channels); start += 50 {
		end := min(start+50, len(channels))
		chunk := channels[start:end]
		if err := tx.Create(&chunk).Error; err != nil {
			tx.Rollback()
			return err
		}
		for _, channel_ := range chunk {
			if err := channel_.AddAbilities(tx); err != nil {
				tx.Rollback()
				return err
			}
		}
	}
	if audit {
		ids := make([]int, 0, len(channels))
		for i := range channels {
			ids = append(ids, channels[i].Id)
		}
		var afterChannels []Channel
		if err := tx.Where("id in (?)", ids).Find(&afterChannels).Error; err != nil {
			tx.Rollback()
			return err
		}
		pairs := make([]ChannelAuditPair, 0, len(afterChannels))
		for i := range afterChannels {
			pairs = append(pairs, ChannelAuditPair{After: &afterChannels[i]})
		}
		if err := RecordChannelAuditPairs(tx, actor, source, batchID, pairs); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit().Error
}

func BatchDeleteChannels(ids []int) error {
	return batchDeleteChannels(ids, true, systemChannelAuditActor(), "channel_delete_internal", common.GetUUID())
}

func BatchDeleteChannelsWithAudit(ids []int, actor ChannelAuditActor, source string, batchID string) error {
	return batchDeleteChannels(ids, true, actor, source, batchID)
}

func batchDeleteChannels(ids []int, audit bool, actor ChannelAuditActor, source string, batchID string) error {
	if len(ids) == 0 {
		return nil
	}
	// 使用事务 分批删除channel表和abilities表
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	var beforeChannels []Channel
	if audit {
		if err := WithChannelMutationLock(tx).Where("id in (?)", ids).Order("id asc").Find(&beforeChannels).Error; err != nil {
			tx.Rollback()
			return err
		}
	}
	for _, chunk := range lo.Chunk(ids, 200) {
		if err := tx.Where("id in (?)", chunk).Delete(&Channel{}).Error; err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Where("channel_id in (?)", chunk).Delete(&Ability{}).Error; err != nil {
			tx.Rollback()
			return err
		}
	}
	if audit {
		pairs := make([]ChannelAuditPair, 0, len(beforeChannels))
		for i := range beforeChannels {
			pairs = append(pairs, ChannelAuditPair{Before: &beforeChannels[i]})
		}
		if err := RecordChannelAuditPairs(tx, actor, source, batchID, pairs); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit().Error
}

func (channel *Channel) GetPriority() int64 {
	if channel.Priority == nil {
		return 0
	}
	return *channel.Priority
}

func (channel *Channel) GetWeight() int {
	if channel.Weight == nil {
		return 0
	}
	return int(*channel.Weight)
}

func (channel *Channel) GetMaxContextTokens() int {
	if channel == nil || channel.MaxContextTokens == nil {
		return 0
	}
	return *channel.MaxContextTokens
}

func (channel *Channel) GetMaxOutputTokens() int {
	if channel == nil || channel.MaxOutputTokens == nil {
		return 0
	}
	return *channel.MaxOutputTokens
}

func (channel *Channel) GetMinInputTokens() int {
	if channel == nil || channel.MinInputTokens == nil {
		return 0
	}
	return *channel.MinInputTokens
}

func (channel *Channel) GetMaxInputTokens() int {
	if channel == nil || channel.MaxInputTokens == nil {
		return 0
	}
	return *channel.MaxInputTokens
}

func (channel *Channel) GetBaseURL() string {
	if channel.BaseURL == nil {
		return ""
	}
	url := *channel.BaseURL
	if url == "" {
		url = constant.ChannelBaseURLs[channel.Type]
	}
	return url
}

func (channel *Channel) GetModelMapping() string {
	if channel.ModelMapping == nil {
		return ""
	}
	return *channel.ModelMapping
}

func (channel *Channel) GetStatusCodeMapping() string {
	if channel.StatusCodeMapping == nil {
		return ""
	}
	return *channel.StatusCodeMapping
}

func (channel *Channel) Insert() error {
	channels := []Channel{*channel}
	if err := BatchInsertChannels(channels); err != nil {
		return err
	}
	*channel = channels[0]
	return nil
}

func (channel *Channel) Update() error {
	return channel.UpdateWithAudit(systemChannelAuditActor(), "channel_update_internal", "")
}

func (channel *Channel) UpdateWithAudit(actor ChannelAuditActor, source string, batchID string) error {
	return channel.UpdateWithAuditAction(actor, source, batchID, ChannelAuditActionUpdate)
}

func (channel *Channel) UpdateWithAuditAction(actor ChannelAuditActor, source string, batchID string, action string) error {
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	before := &Channel{}
	if err := WithChannelMutationLock(tx).First(before, "id = ?", channel.Id).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := channel.updateWithDB(tx, tx); err != nil {
		tx.Rollback()
		return err
	}
	after := &Channel{}
	if err := tx.First(after, "id = ?", channel.Id).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := RecordChannelAuditPairs(tx, actor, source, batchID, []ChannelAuditPair{{Before: before, After: after, Action: action}}); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	*channel = *after
	return nil
}

// updateWithDB persists a channel using the supplied database handle. When
// abilityTx is non-nil, derived abilities are updated in the same transaction
// as the channel row (used by audited configuration mutations).
func (channel *Channel) updateWithDB(useDB *gorm.DB, abilityTx *gorm.DB) error {
	if err := channel.ValidateSettings(); err != nil {
		return err
	}
	// If this is a multi-key channel, recalculate MultiKeySize based on the current key list to avoid inconsistency after editing keys
	if channel.ChannelInfo.IsMultiKey {
		var keyStr string
		if channel.Key != "" {
			keyStr = channel.Key
		} else {
			// If key is not provided, read the existing key from the database
			existing := Channel{}
			if err := useDB.First(&existing, "id = ?", channel.Id).Error; err == nil {
				keyStr = existing.Key
			}
		}
		// Parse the key list (supports newline separation or JSON array)
		keys := []string{}
		if keyStr != "" {
			trimmed := strings.TrimSpace(keyStr)
			if strings.HasPrefix(trimmed, "[") {
				var arr []json.RawMessage
				if err := common.Unmarshal([]byte(trimmed), &arr); err == nil {
					keys = make([]string, len(arr))
					for i, v := range arr {
						keys[i] = string(v)
					}
				}
			}
			if len(keys) == 0 { // fallback to newline split
				keys = strings.Split(strings.Trim(keyStr, "\n"), "\n")
			}
		}
		channel.ChannelInfo.MultiKeySize = len(keys)
		// Clean up status data that exceeds the new key count to prevent index out of range
		if channel.ChannelInfo.MultiKeyStatusList != nil {
			for idx := range channel.ChannelInfo.MultiKeyStatusList {
				if idx >= channel.ChannelInfo.MultiKeySize {
					delete(channel.ChannelInfo.MultiKeyStatusList, idx)
				}
			}
		}
	}
	var err error
	err = useDB.Model(channel).Updates(channel).Error
	if err != nil {
		return err
	}
	if err = useDB.Model(channel).First(channel, "id = ?", channel.Id).Error; err != nil {
		return err
	}
	err = channel.UpdateAbilities(abilityTx)
	return err
}

func (channel *Channel) UpdateResponseTime(responseTime int64) {
	err := DB.Model(channel).Select("response_time", "test_time").Updates(Channel{
		TestTime:     common.GetTimestamp(),
		ResponseTime: int(responseTime),
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update response time: channel_id=%d, error=%v", channel.Id, err))
	}
}

func (channel *Channel) UpdateBalance(balance float64) {
	err := DB.Model(channel).Select("balance_updated_time", "balance").Updates(Channel{
		BalanceUpdatedTime: common.GetTimestamp(),
		Balance:            balance,
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update balance: channel_id=%d, error=%v", channel.Id, err))
	}
}

func (channel *Channel) Delete() error {
	return channel.DeleteWithAudit(systemChannelAuditActor(), "channel_delete_internal", "")
}

func (channel *Channel) DeleteWithAudit(actor ChannelAuditActor, source string, batchID string) error {
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	before := &Channel{}
	if err := WithChannelMutationLock(tx).First(before, "id = ?", channel.Id).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Delete(&Channel{}, "id = ?", channel.Id).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := RecordChannelAuditPairs(tx, actor, source, batchID, []ChannelAuditPair{{Before: before}}); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit().Error
}

var channelStatusLock sync.Mutex

// channelPollingLocks stores locks for each channel.id to ensure thread-safe polling
var channelPollingLocks sync.Map

// GetChannelPollingLock returns or creates a mutex for the given channel ID
func GetChannelPollingLock(channelId int) *sync.Mutex {
	if lock, exists := channelPollingLocks.Load(channelId); exists {
		return lock.(*sync.Mutex)
	}
	// Create new lock for this channel
	newLock := &sync.Mutex{}
	actual, _ := channelPollingLocks.LoadOrStore(channelId, newLock)
	return actual.(*sync.Mutex)
}

// CleanupChannelPollingLocks removes locks for channels that no longer exist
// This is optional and can be called periodically to prevent memory leaks
func CleanupChannelPollingLocks() {
	var activeChannelIds []int
	DB.Model(&Channel{}).Pluck("id", &activeChannelIds)

	activeChannelSet := make(map[int]bool)
	for _, id := range activeChannelIds {
		activeChannelSet[id] = true
	}

	channelPollingLocks.Range(func(key, value interface{}) bool {
		channelId := key.(int)
		if !activeChannelSet[channelId] {
			channelPollingLocks.Delete(channelId)
		}
		return true
	})
}

func handlerMultiKeyUpdate(channel *Channel, usingKey string, status int, reason string) {
	keys := channel.GetKeys()
	if len(keys) == 0 {
		channel.Status = status
	} else {
		var keyIndex int
		for i, key := range keys {
			if key == usingKey {
				keyIndex = i
				break
			}
		}
		if channel.ChannelInfo.MultiKeyStatusList == nil {
			channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
		}
		if status == common.ChannelStatusEnabled {
			delete(channel.ChannelInfo.MultiKeyStatusList, keyIndex)
		} else {
			channel.ChannelInfo.MultiKeyStatusList[keyIndex] = status
			if channel.ChannelInfo.MultiKeyDisabledReason == nil {
				channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
			}
			if channel.ChannelInfo.MultiKeyDisabledTime == nil {
				channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
			}
			channel.ChannelInfo.MultiKeyDisabledReason[keyIndex] = reason
			channel.ChannelInfo.MultiKeyDisabledTime[keyIndex] = common.GetTimestamp()
		}
		if len(channel.ChannelInfo.MultiKeyStatusList) >= channel.ChannelInfo.MultiKeySize {
			channel.Status = common.ChannelStatusAutoDisabled
			info := channel.GetOtherInfo()
			info["status_reason"] = "All keys are disabled"
			info["status_time"] = common.GetTimestamp()
			channel.SetOtherInfo(info)
		}
	}
}

func UpdateChannelStatus(channelId int, usingKey string, status int, reason string) bool {
	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()

		channelCache, _ := CacheGetChannel(channelId)
		if channelCache == nil {
			return false
		}
		if channelCache.ChannelInfo.IsMultiKey {
			// Use per-channel lock to prevent concurrent map read/write with GetNextEnabledKey
			pollingLock := GetChannelPollingLock(channelId)
			pollingLock.Lock()
			// 如果是多Key模式，更新缓存中的状态
			handlerMultiKeyUpdate(channelCache, usingKey, status, reason)
			pollingLock.Unlock()
			//CacheUpdateChannel(channelCache)
			//return true
		} else {
			// 如果缓存渠道存在，且状态已是目标状态，直接返回
			if channelCache.Status == status {
				return false
			}
			CacheUpdateChannelStatus(channelId, status)
		}
	}

	shouldUpdateAbilities := false
	defer func() {
		if shouldUpdateAbilities {
			err := UpdateAbilityStatus(channelId, status == common.ChannelStatusEnabled)
			if err != nil {
				common.SysLog(fmt.Sprintf("failed to update ability status: channel_id=%d, error=%v", channelId, err))
			}
		}
	}()
	channel, err := GetChannelById(channelId, true)
	if err != nil {
		return false
	} else {
		if channel.Status == status {
			return false
		}

		if channel.ChannelInfo.IsMultiKey {
			beforeStatus := channel.Status
			// Protect map writes with the same per-channel lock used by readers
			pollingLock := GetChannelPollingLock(channelId)
			pollingLock.Lock()
			handlerMultiKeyUpdate(channel, usingKey, status, reason)
			pollingLock.Unlock()
			if beforeStatus != channel.Status {
				shouldUpdateAbilities = true
			}
		} else {
			info := channel.GetOtherInfo()
			info["status_reason"] = reason
			info["status_time"] = common.GetTimestamp()
			channel.SetOtherInfo(info)
			channel.Status = status
			shouldUpdateAbilities = true
		}
		err = channel.saveWithoutKeyWithAudit(systemChannelAuditActor(), "automatic_status", "", ChannelAuditActionStatusChange)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to update channel status: channel_id=%d, status=%d, error=%v", channel.Id, status, err))
			return false
		}
	}
	return true
}

func EnableChannelByTag(tag string) error {
	return UpdateChannelStatusByTagWithAudit(tag, common.ChannelStatusEnabled, systemChannelAuditActor(), "channel_tag_enable_internal", common.GetUUID())
}

func DisableChannelByTag(tag string) error {
	return UpdateChannelStatusByTagWithAudit(tag, common.ChannelStatusManuallyDisabled, systemChannelAuditActor(), "channel_tag_disable_internal", common.GetUUID())
}

func UpdateChannelStatusByTagWithAudit(tag string, status int, actor ChannelAuditActor, source string, batchID string) error {
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	var beforeChannels []Channel
	if err := WithChannelMutationLock(tx).Where("tag = ?", tag).Order("id asc").Find(&beforeChannels).Error; err != nil {
		tx.Rollback()
		return err
	}
	if len(beforeChannels) == 0 {
		return tx.Commit().Error
	}
	ids := make([]int, 0, len(beforeChannels))
	for i := range beforeChannels {
		ids = append(ids, beforeChannels[i].Id)
	}
	if err := tx.Model(&Channel{}).Where("id in (?)", ids).Update("status", status).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Model(&Ability{}).Where("channel_id in (?)", ids).Select("enabled").Update("enabled", status == common.ChannelStatusEnabled).Error; err != nil {
		tx.Rollback()
		return err
	}
	var afterChannels []Channel
	if err := tx.Where("id in (?)", ids).Find(&afterChannels).Error; err != nil {
		tx.Rollback()
		return err
	}
	pairs := pairChannelAuditSnapshots(beforeChannels, afterChannels)
	for i := range pairs {
		pairs[i].Action = ChannelAuditActionStatusChange
	}
	if err := RecordChannelAuditPairs(tx, actor, source, batchID, pairs); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit().Error
}

func pairChannelAuditSnapshots(beforeChannels []Channel, afterChannels []Channel) []ChannelAuditPair {
	afterByID := make(map[int]*Channel, len(afterChannels))
	for i := range afterChannels {
		afterByID[afterChannels[i].Id] = &afterChannels[i]
	}
	pairs := make([]ChannelAuditPair, 0, len(beforeChannels))
	for i := range beforeChannels {
		pairs = append(pairs, ChannelAuditPair{
			Before: &beforeChannels[i],
			After:  afterByID[beforeChannels[i].Id],
		})
	}
	return pairs
}

func EditChannelByTag(tag string, newTag *string, modelMapping *string, models *string, group *string, priority *int64, weight *uint, paramOverride *string, headerOverride *string) error {
	return EditChannelByTagWithAudit(tag, newTag, modelMapping, models, group, priority, weight, paramOverride, headerOverride, systemChannelAuditActor(), "channel_tag_edit_internal", common.GetUUID())
}

func EditChannelByTagWithAudit(tag string, newTag *string, modelMapping *string, models *string, group *string, priority *int64, weight *uint, paramOverride *string, headerOverride *string, actor ChannelAuditActor, source string, batchID string) error {
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	var beforeChannels []Channel
	if err := WithChannelMutationLock(tx).Where("tag = ?", tag).Order("id asc").Find(&beforeChannels).Error; err != nil {
		tx.Rollback()
		return err
	}
	if len(beforeChannels) == 0 {
		return tx.Commit().Error
	}
	ids := make([]int, 0, len(beforeChannels))
	for i := range beforeChannels {
		ids = append(ids, beforeChannels[i].Id)
	}

	updateData := Channel{}
	shouldReCreateAbilities := false
	if newTag != nil && *newTag != tag {
		updateData.Tag = newTag
	}
	if modelMapping != nil && *modelMapping != "" {
		updateData.ModelMapping = modelMapping
	}
	if models != nil && *models != "" {
		shouldReCreateAbilities = true
		updateData.Models = *models
	}
	if group != nil && *group != "" {
		shouldReCreateAbilities = true
		updateData.Group = *group
	}
	if priority != nil {
		updateData.Priority = priority
	}
	if weight != nil {
		updateData.Weight = weight
	}
	if paramOverride != nil {
		updateData.ParamOverride = paramOverride
	}
	if headerOverride != nil {
		updateData.HeaderOverride = headerOverride
	}
	if err := tx.Model(&Channel{}).Where("id in (?)", ids).Updates(updateData).Error; err != nil {
		tx.Rollback()
		return err
	}

	var afterChannels []Channel
	if err := tx.Where("id in (?)", ids).Find(&afterChannels).Error; err != nil {
		tx.Rollback()
		return err
	}
	if shouldReCreateAbilities {
		for i := range afterChannels {
			if err := afterChannels[i].UpdateAbilities(tx); err != nil {
				tx.Rollback()
				return err
			}
		}
	} else {
		ability := Ability{}
		if newTag != nil {
			ability.Tag = newTag
		}
		if priority != nil {
			ability.Priority = priority
		}
		if weight != nil {
			ability.Weight = *weight
		}
		if err := tx.Model(&Ability{}).Where("channel_id in (?)", ids).Updates(ability).Error; err != nil {
			tx.Rollback()
			return err
		}
	}
	pairs := pairChannelAuditSnapshots(beforeChannels, afterChannels)
	for i := range pairs {
		pairs[i].Action = ChannelAuditActionTagUpdate
	}
	if err := RecordChannelAuditPairs(tx, actor, source, batchID, pairs); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit().Error
}

func UpdateChannelUsedQuota(id int, quota int) {
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeChannelUsedQuota, id, quota)
		return
	}
	updateChannelUsedQuota(id, quota)
}

func updateChannelUsedQuota(id int, quota int) {
	err := DB.Model(&Channel{}).Where("id = ?", id).Update("used_quota", gorm.Expr("used_quota + ?", quota)).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update channel used quota: channel_id=%d, delta_quota=%d, error=%v", id, quota, err))
	}
}

func DeleteChannelByStatus(status int64) (int64, error) {
	return deleteChannelsByStatusWithAudit(
		[]int64{status},
		systemChannelAuditActor(),
		"channel_delete_by_status_internal",
		common.GetUUID(),
	)
}

func DeleteDisabledChannel() (int64, error) {
	return DeleteDisabledChannelWithAudit(systemChannelAuditActor(), "channel_delete_disabled_internal", common.GetUUID())
}

func DeleteDisabledChannelWithAudit(actor ChannelAuditActor, source string, batchID string) (int64, error) {
	return deleteChannelsByStatusWithAudit(
		[]int64{common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled},
		actor,
		source,
		batchID,
	)
}

func deleteChannelsByStatusWithAudit(statuses []int64, actor ChannelAuditActor, source string, batchID string) (int64, error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	var beforeChannels []Channel
	if err := WithChannelMutationLock(tx).Where("status in (?)", statuses).Order("id asc").Find(&beforeChannels).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	if len(beforeChannels) == 0 {
		if err := tx.Commit().Error; err != nil {
			return 0, err
		}
		return 0, nil
	}

	ids := make([]int, 0, len(beforeChannels))
	pairs := make([]ChannelAuditPair, 0, len(beforeChannels))
	for i := range beforeChannels {
		ids = append(ids, beforeChannels[i].Id)
		pairs = append(pairs, ChannelAuditPair{Before: &beforeChannels[i]})
	}
	result := tx.Where("id in (?)", ids).Delete(&Channel{})
	if result.Error != nil {
		tx.Rollback()
		return 0, result.Error
	}
	if err := tx.Where("channel_id in (?)", ids).Delete(&Ability{}).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := RecordChannelAuditPairs(tx, actor, source, batchID, pairs); err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return result.RowsAffected, nil
}

func GetPaginatedTags(offset int, limit int) ([]*string, error) {
	var tags []*string
	err := DB.Model(&Channel{}).Select("DISTINCT tag").Where("tag != ''").Offset(offset).Limit(limit).Find(&tags).Error
	return tags, err
}

func SearchTags(keyword string, group string, model string, idSort bool) ([]*string, error) {
	var tags []*string
	modelsCol := "`models`"

	// 如果是 PostgreSQL，使用双引号
	if common.UsingPostgreSQL {
		modelsCol = `"models"`
	}

	baseURLCol := "`base_url`"
	// 如果是 PostgreSQL，使用双引号
	if common.UsingPostgreSQL {
		baseURLCol = `"base_url"`
	}

	order := "priority desc"
	if idSort {
		order = "id desc"
	}

	// 构造基础查询
	baseQuery := DB.Model(&Channel{}).Omit("key")

	// 构造WHERE子句
	var whereClause string
	var args []interface{}
	if group != "" && group != "null" {
		var groupCondition string
		if common.UsingMySQL {
			groupCondition = `CONCAT(',', ` + commonGroupCol + `, ',') LIKE ?`
		} else {
			// sqlite, PostgreSQL
			groupCondition = `(',' || ` + commonGroupCol + ` || ',') LIKE ?`
		}
		whereClause = "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + ` LIKE ? AND ` + groupCondition
		args = append(args, common.String2Int(keyword), "%"+keyword+"%", keyword, "%"+keyword+"%", "%"+model+"%", "%,"+group+",%")
	} else {
		whereClause = "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
		args = append(args, common.String2Int(keyword), "%"+keyword+"%", keyword, "%"+keyword+"%", "%"+model+"%")
	}

	subQuery := baseQuery.Where(whereClause, args...).
		Select("tag").
		Where("tag != ''").
		Order(order)

	err := DB.Table("(?) as sub", subQuery).
		Select("DISTINCT tag").
		Find(&tags).Error

	if err != nil {
		return nil, err
	}

	return tags, nil
}

func (channel *Channel) ValidateSettings() error {
	channelParams := &dto.ChannelSettings{}
	if channel.Setting != nil && *channel.Setting != "" {
		settingBytes := []byte(*channel.Setting)
		if err := common.Unmarshal(settingBytes, channelParams); err != nil {
			return err
		}
		if channelParams.CacheEnabled && channelParams.NoCacheEnabled {
			return errors.New("cache_enabled and no_cache_enabled cannot both be enabled")
		}
		if channelParams.CacheEnabled || channelParams.CachePercentageMin != nil || channelParams.CachePercentageMax != nil {
			minPercentage, maxPercentage := channelParams.GetCachePercentageRange()
			if minPercentage < 0 || minPercentage > 100 {
				return fmt.Errorf("cache_percentage_min must be between 0 and 100, got %d", minPercentage)
			}
			if maxPercentage < 0 || maxPercentage > 100 {
				return fmt.Errorf("cache_percentage_max must be between 0 and 100, got %d", maxPercentage)
			}
			if minPercentage > maxPercentage {
				return fmt.Errorf("cache_percentage_min must not exceed cache_percentage_max: %d > %d", minPercentage, maxPercentage)
			}
		}

		// thinking_to_content has been retired. Strip it from every write path so
		// legacy clients cannot persist or reactivate the old conversion behavior.
		settingMap := make(map[string]json.RawMessage)
		if err := common.Unmarshal(settingBytes, &settingMap); err != nil {
			return err
		}
		if _, exists := settingMap["thinking_to_content"]; !exists {
			return nil
		}
		delete(settingMap, "thinking_to_content")
		normalizedBytes, err := common.Marshal(settingMap)
		if err != nil {
			return err
		}
		channel.Setting = common.GetPointer(string(normalizedBytes))
	}
	return nil
}

func (channel *Channel) GetSetting() dto.ChannelSettings {
	setting := dto.ChannelSettings{}
	if channel.Setting != nil && *channel.Setting != "" {
		err := common.Unmarshal([]byte(*channel.Setting), &setting)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal setting: channel_id=%d, error=%v", channel.Id, err))
			channel.Setting = nil // 清空设置以避免后续错误
			_ = channel.saveWithAudit(systemChannelAuditActor(), "channel_setting_repair", "", ChannelAuditActionSystemRepair)
		}
	}
	return setting
}

func (channel *Channel) SetSetting(setting dto.ChannelSettings) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal setting: channel_id=%d, error=%v", channel.Id, err))
		return
	}
	channel.Setting = common.GetPointer[string](string(settingBytes))
}

func (channel *Channel) GetOtherSettings() dto.ChannelOtherSettings {
	setting := dto.ChannelOtherSettings{}
	if channel.OtherSettings != "" {
		err := common.UnmarshalJsonStr(channel.OtherSettings, &setting)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal setting: channel_id=%d, error=%v", channel.Id, err))
			channel.OtherSettings = "{}" // 清空设置以避免后续错误
			_ = channel.saveWithAudit(systemChannelAuditActor(), "channel_other_settings_repair", "", ChannelAuditActionSystemRepair)
		}
	}
	return setting
}

func (channel *Channel) SetOtherSettings(setting dto.ChannelOtherSettings) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal setting: channel_id=%d, error=%v", channel.Id, err))
		return
	}
	channel.OtherSettings = string(settingBytes)
}

func (channel *Channel) GetParamOverride() map[string]interface{} {
	paramOverride := make(map[string]interface{})
	if channel.ParamOverride != nil && *channel.ParamOverride != "" {
		err := common.Unmarshal([]byte(*channel.ParamOverride), &paramOverride)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal param override: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return paramOverride
}

func (channel *Channel) GetHeaderOverride() map[string]interface{} {
	headerOverride := make(map[string]interface{})
	if channel.HeaderOverride != nil && *channel.HeaderOverride != "" {
		err := common.Unmarshal([]byte(*channel.HeaderOverride), &headerOverride)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal header override: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return headerOverride
}

func GetChannelsByIds(ids []int) ([]*Channel, error) {
	var channels []*Channel
	err := DB.Where("id in (?)", ids).Find(&channels).Error
	return channels, err
}

func BatchSetChannelTag(ids []int, tag *string) error {
	return BatchSetChannelTagWithAudit(ids, tag, systemChannelAuditActor(), "channel_batch_set_tag_internal", common.GetUUID())
}

func BatchSetChannelTagWithAudit(ids []int, tag *string, actor ChannelAuditActor, source string, batchID string) error {
	if len(ids) == 0 {
		return nil
	}
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	var beforeChannels []Channel
	if err := WithChannelMutationLock(tx).Where("id in (?)", ids).Order("id asc").Find(&beforeChannels).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Model(&Channel{}).Where("id in (?)", ids).Update("tag", tag).Error; err != nil {
		tx.Rollback()
		return err
	}
	var afterChannels []Channel
	if err := tx.Where("id in (?)", ids).Find(&afterChannels).Error; err != nil {
		tx.Rollback()
		return err
	}
	for i := range afterChannels {
		if err := afterChannels[i].UpdateAbilities(tx); err != nil {
			tx.Rollback()
			return err
		}
	}
	pairs := pairChannelAuditSnapshots(beforeChannels, afterChannels)
	for i := range pairs {
		pairs[i].Action = ChannelAuditActionTagUpdate
	}
	if err := RecordChannelAuditPairs(tx, actor, source, batchID, pairs); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit().Error
}

// CountAllChannels returns total channels in DB
func CountAllChannels() (int64, error) {
	var total int64
	err := DB.Model(&Channel{}).Count(&total).Error
	return total, err
}

// CountAllTags returns number of non-empty distinct tags
func CountAllTags() (int64, error) {
	var total int64
	err := DB.Model(&Channel{}).Where("tag is not null AND tag != ''").Distinct("tag").Count(&total).Error
	return total, err
}

// Get channels of specified type with pagination
func GetChannelsByType(startIdx int, num int, idSort bool, channelType int) ([]*Channel, error) {
	var channels []*Channel
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	err := DB.Where("type = ?", channelType).Order(order).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	return channels, err
}

// Count channels of specific type
func CountChannelsByType(channelType int) (int64, error) {
	var count int64
	err := DB.Model(&Channel{}).Where("type = ?", channelType).Count(&count).Error
	return count, err
}

// Return map[type]count for all channels
func CountChannelsGroupByType() (map[int64]int64, error) {
	type result struct {
		Type  int64 `gorm:"column:type"`
		Count int64 `gorm:"column:count"`
	}
	var results []result
	err := DB.Model(&Channel{}).Select("type, count(*) as count").Group("type").Find(&results).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[int64]int64)
	for _, r := range results {
		counts[r.Type] = r.Count
	}
	return counts, nil
}
