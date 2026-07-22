package model

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	ChannelAuditActionCreate           = "create"
	ChannelAuditActionUpdate           = "update"
	ChannelAuditActionDelete           = "delete"
	ChannelAuditActionStatusChange     = "status_change"
	ChannelAuditActionTagUpdate        = "tag_update"
	ChannelAuditActionKeyManage        = "key_manage"
	ChannelAuditActionModelSync        = "model_sync"
	ChannelAuditActionCredentialChange = "credential_change"
	ChannelAuditActionSystemRepair     = "system_repair"

	ChannelAuditActorAdmin  = "admin"
	ChannelAuditActorSystem = "system"

	channelAuditSnapshotVersion = 1
	channelAuditRedactedValue   = "[REDACTED]"
	channelAuditUnparsedValue   = "[UNPARSED VALUE OMITTED]"
	channelAuditInvalidURLValue = "[INVALID URL OMITTED]"
)

// ChannelAuditLog is an immutable, sanitized record of a channel mutation.
// It deliberately lives in the primary database so callers can write it in the
// same transaction as the channel change.
type ChannelAuditLog struct {
	Id              int64  `json:"id" gorm:"primaryKey"`
	ChannelId       int    `json:"channel_id" gorm:"index:idx_channel_audit_channel_created,priority:1;index"`
	ChannelName     string `json:"channel_name" gorm:"type:varchar(255)"`
	ChannelType     int    `json:"channel_type" gorm:"index"`
	Action          string `json:"action" gorm:"type:varchar(32);index"`
	Source          string `json:"source" gorm:"type:varchar(64);index"`
	OperatorType    string `json:"operator_type" gorm:"type:varchar(16);index"`
	OperatorId      int    `json:"operator_id" gorm:"index"`
	OperatorName    string `json:"operator_name" gorm:"type:varchar(255)"`
	OperatorRole    int    `json:"operator_role"`
	RequestId       string `json:"request_id" gorm:"type:varchar(128);index"`
	Ip              string `json:"ip" gorm:"type:varchar(64);index"`
	UserAgent       string `json:"user_agent" gorm:"type:text"`
	Method          string `json:"method" gorm:"type:varchar(16)"`
	Path            string `json:"path" gorm:"type:varchar(1024)"`
	BatchId         string `json:"batch_id" gorm:"type:varchar(128);index"`
	CreatedAt       int64  `json:"created_at" gorm:"bigint;index;index:idx_channel_audit_channel_created,priority:2"`
	BeforeSnapshot  []byte `json:"-"`
	AfterSnapshot   []byte `json:"-"`
	Changes         []byte `json:"-"`
	ChangedFields   []byte `json:"-"`
	ChangeCount     int    `json:"change_count"`
	SnapshotVersion int    `json:"snapshot_version" gorm:"default:1"`
}

// ChannelAuditActor describes either an authenticated administrator or a
// named system task responsible for a mutation.
type ChannelAuditActor struct {
	Type      string
	ID        int
	Name      string
	Role      int
	RequestID string
	IP        string
	UserAgent string
	Method    string
	Path      string
}

// ChannelAuditPair contains the actual database state before and after one
// channel mutation. A nil Before means create; a nil After means delete.
type ChannelAuditPair struct {
	Before *Channel
	After  *Channel
	Action string
}

// ChannelAuditChange is the stable JSON shape consumed by the audit detail UI.
// Before and After are always sanitized values.
type ChannelAuditChange struct {
	Field     string `json:"field"`
	Before    any    `json:"before"`
	After     any    `json:"after"`
	Sensitive bool   `json:"sensitive"`
}

// ChannelAuditQuery contains cross-database-safe filters for the audit list.
type ChannelAuditQuery struct {
	ChannelID    int
	ChannelName  string
	Action       string
	Source       string
	OperatorName string
	StartTime    int64
	EndTime      int64
}

type channelAuditUnparsed struct {
	raw string
}

type channelAuditMissing struct{}

type channelAuditRedactor struct {
	secrets []string
}

var channelAuditURLPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s"'<>]+`)

func auditPointerValue[T any](value *T) any {
	if value == nil {
		return nil
	}
	return *value
}

func auditNormalizeList(value string) []string {
	seen := make(map[string]struct{})
	items := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		items = append(items, item)
	}
	sort.Strings(items)
	return items
}

func auditIntMap[T any](values map[int]T) any {
	if values == nil {
		return nil
	}
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[strconv.Itoa(key)] = value
	}
	return result
}

func auditDecodeJSON(raw string) (any, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", true
	}
	message := json.RawMessage(trimmed)
	switch common.GetJsonType(message) {
	case "object":
		var source map[string]json.RawMessage
		if err := common.Unmarshal(message, &source); err != nil {
			return nil, false
		}
		result := make(map[string]any, len(source))
		for key, child := range source {
			decoded, ok := auditDecodeJSON(string(child))
			if !ok {
				return nil, false
			}
			result[key] = decoded
		}
		return result, true
	case "array":
		var source []json.RawMessage
		if err := common.Unmarshal(message, &source); err != nil {
			return nil, false
		}
		result := make([]any, 0, len(source))
		for _, child := range source {
			decoded, ok := auditDecodeJSON(string(child))
			if !ok {
				return nil, false
			}
			result = append(result, decoded)
		}
		return result, true
	case "string":
		var value string
		if err := common.Unmarshal(message, &value); err != nil {
			return nil, false
		}
		return value, true
	case "boolean":
		var value bool
		if err := common.Unmarshal(message, &value); err != nil {
			return nil, false
		}
		return value, true
	case "null":
		var value any
		if err := common.Unmarshal(message, &value); err != nil {
			return nil, false
		}
		return nil, true
	case "number":
		var value json.RawMessage
		if err := common.Unmarshal(message, &value); err != nil {
			return nil, false
		}
		return value, true
	default:
		return nil, false
	}
}

func auditJSONField(raw string) any {
	value, ok := auditDecodeJSON(raw)
	if !ok {
		return channelAuditUnparsed{raw: raw}
	}
	return value
}

func auditSettingsField(raw string) any {
	value := auditJSONField(raw)
	settings, ok := value.(map[string]any)
	if !ok {
		return value
	}
	// These values are detection runtime state rather than channel
	// configuration. Recording every check would flood the audit table.
	delete(settings, "upstream_model_update_last_check_time")
	return settings
}

func channelAuditRawSnapshot(channel *Channel) map[string]any {
	if channel == nil {
		return nil
	}
	return map[string]any{
		"id":                  channel.Id,
		"type":                channel.Type,
		"key":                 channel.Key,
		"openai_organization": auditPointerValue(channel.OpenAIOrganization),
		"test_model":          auditPointerValue(channel.TestModel),
		"status":              channel.Status,
		"name":                channel.Name,
		"weight":              auditPointerValue(channel.Weight),
		"max_context_tokens":  auditPointerValue(channel.MaxContextTokens),
		"max_output_tokens":   auditPointerValue(channel.MaxOutputTokens),
		"min_input_tokens":    auditPointerValue(channel.MinInputTokens),
		"max_input_tokens":    auditPointerValue(channel.MaxInputTokens),
		"created_time":        channel.CreatedTime,
		"base_url":            auditPointerValue(channel.BaseURL),
		"other":               auditJSONField(channel.Other),
		"models":              auditNormalizeList(channel.Models),
		"group":               auditNormalizeList(channel.Group),
		"model_mapping":       auditJSONField(pointerStringValue(channel.ModelMapping)),
		"status_code_mapping": auditJSONField(pointerStringValue(channel.StatusCodeMapping)),
		"priority":            auditPointerValue(channel.Priority),
		"auto_ban":            auditPointerValue(channel.AutoBan),
		"other_info":          auditJSONField(channel.OtherInfo),
		"tag":                 auditPointerValue(channel.Tag),
		"setting":             auditJSONField(pointerStringValue(channel.Setting)),
		"param_override":      auditJSONField(pointerStringValue(channel.ParamOverride)),
		"header_override":     auditJSONField(pointerStringValue(channel.HeaderOverride)),
		"remark":              auditPointerValue(channel.Remark),
		"settings":            auditSettingsField(channel.OtherSettings),
		"channel_info": map[string]any{
			"is_multi_key":              channel.ChannelInfo.IsMultiKey,
			"multi_key_size":            channel.ChannelInfo.MultiKeySize,
			"multi_key_status_list":     auditIntMap(channel.ChannelInfo.MultiKeyStatusList),
			"multi_key_disabled_reason": auditIntMap(channel.ChannelInfo.MultiKeyDisabledReason),
			"multi_key_disabled_time":   auditIntMap(channel.ChannelInfo.MultiKeyDisabledTime),
			"multi_key_mode":            channel.ChannelInfo.MultiKeyMode,
		},
	}
}

func pointerStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func auditFieldName(path string) string {
	if index := strings.LastIndex(path, "."); index >= 0 {
		path = path[index+1:]
	}
	if index := strings.Index(path, "["); index >= 0 {
		path = path[:index]
	}
	return strings.ToLower(strings.TrimSpace(path))
}

func auditCompactName(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

func auditSensitiveName(name string) bool {
	compact := auditCompactName(name)
	switch compact {
	case "key", "keys", "auth", "authentication", "authorization", "proxyauthorization",
		"apikey", "xapikey", "cookie", "setcookie", "bearer", "token", "secret",
		"password", "passwd", "credential", "credentials", "privatekey", "signature":
		return true
	}
	for _, suffix := range []string{
		"token", "secret", "password", "passwd", "credential", "credentials",
		"signature", "authorization",
	} {
		if strings.HasSuffix(compact, suffix) {
			return true
		}
	}
	return strings.Contains(compact, "apikey") ||
		strings.Contains(compact, "accesskey") ||
		strings.Contains(compact, "privatekey") ||
		strings.Contains(compact, "secretkey") ||
		strings.Contains(compact, "signingkey") ||
		strings.Contains(compact, "encryptionkey")
}

func auditSensitiveURLQueryName(name string) bool {
	if auditSensitiveName(name) {
		return true
	}
	compact := auditCompactName(name)
	switch compact {
	case "key", "sig", "code", "auth", "authentication", "sas", "subscriptionkey",
		"accesskeyid", "awsaccesskeyid", "xamzcredential", "xamzsignature",
		"xgoogcredential", "xgoogsignature":
		return true
	}
	return strings.Contains(compact, "accesskey") ||
		strings.Contains(compact, "subscriptionkey") ||
		strings.Contains(compact, "credential") ||
		strings.Contains(compact, "signature") ||
		strings.Contains(compact, "authorization")
}

func auditSensitiveURLPathSegment(previous string, segment string) bool {
	previousCompact := auditCompactName(previous)
	switch previousCompact {
	case "webhook", "webhooks", "hook", "hooks", "token", "tokens", "key", "keys",
		"secret", "secrets", "credential", "credentials", "bot":
		return segment != ""
	}

	compact := auditCompactName(segment)
	if strings.HasPrefix(compact, "bot") && len(compact) >= 12 {
		return true
	}
	if len(segment) < 24 {
		return false
	}
	letters := 0
	digits := 0
	for _, char := range segment {
		switch {
		case unicode.IsLetter(char):
			letters++
		case unicode.IsDigit(char):
			digits++
		case char == '-', char == '_', char == '.', char == '~':
		default:
			return false
		}
	}
	return (letters > 0 && digits > 0) || len(segment) >= 40
}

func auditSensitivePath(path string) bool {
	if path == "key" {
		return true
	}
	lowerPath := strings.ToLower(path)
	if lowerPath == "header_override" || strings.HasPrefix(lowerPath, "header_override.") {
		// Custom header names are unrestricted and their values can carry
		// credentials even when the name itself is not recognizable.
		return true
	}
	if lowerPath == "channel_info.multi_key_disabled_reason" || strings.HasPrefix(lowerPath, "channel_info.multi_key_disabled_reason.") {
		// Reasons may contain raw upstream error messages. Preserve that the
		// field changed without persisting provider-returned credential text.
		return true
	}
	return auditSensitiveName(auditFieldName(path))
}

func auditURLPath(path string) bool {
	name := auditCompactName(auditFieldName(path))
	return name == "proxy" || name == "endpoint" || strings.Contains(name, "url") || strings.Contains(name, "uri")
}

func auditJoinPath(parent string, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func auditCollectScalarStrings(value any, secrets map[string]struct{}) {
	switch typed := value.(type) {
	case string:
		if typed != "" {
			secrets[typed] = struct{}{}
			lower := strings.ToLower(typed)
			for _, prefix := range []string{"bearer ", "basic "} {
				if strings.HasPrefix(lower, prefix) && len(typed) > len(prefix) {
					secrets[strings.TrimSpace(typed[len(prefix):])] = struct{}{}
				}
			}
		}
	case map[string]any:
		for _, child := range typed {
			auditCollectScalarStrings(child, secrets)
		}
	case []any:
		for _, child := range typed {
			auditCollectScalarStrings(child, secrets)
		}
	}
}

func auditCollectURLSecrets(raw string, secrets map[string]struct{}) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return
	}
	if parsed.User != nil {
		if username := parsed.User.Username(); username != "" {
			secrets[username] = struct{}{}
		}
		if password, ok := parsed.User.Password(); ok && password != "" {
			secrets[password] = struct{}{}
		}
	}
	if parsed.Fragment != "" {
		secrets[parsed.Fragment] = struct{}{}
	}
	pathSegments := strings.Split(parsed.Path, "/")
	previousSegment := ""
	for _, segment := range pathSegments {
		if auditSensitiveURLPathSegment(previousSegment, segment) && segment != "" {
			secrets[segment] = struct{}{}
		}
		previousSegment = segment
	}
	query := parsed.Query()
	for key, values := range query {
		if !auditSensitiveURLQueryName(key) {
			continue
		}
		for _, value := range values {
			if value != "" {
				secrets[value] = struct{}{}
			}
		}
	}
}

func auditCollectSecrets(path string, value any, secrets map[string]struct{}) {
	if auditSensitivePath(path) {
		auditCollectScalarStrings(value, secrets)
	}
	switch typed := value.(type) {
	case string:
		if auditURLPath(path) {
			auditCollectURLSecrets(typed, secrets)
		} else if strings.Contains(typed, "://") {
			for _, matchedURL := range channelAuditURLPattern.FindAllString(typed, -1) {
				auditCollectURLSecrets(matchedURL, secrets)
			}
		}
		if path == "key" {
			for _, key := range strings.Split(typed, "\n") {
				key = strings.TrimSpace(key)
				if key != "" {
					secrets[key] = struct{}{}
				}
			}
			if decoded, ok := auditDecodeJSON(typed); ok {
				auditCollectSecrets("key_payload", decoded, secrets)
			}
		}
	case map[string]any:
		for key, child := range typed {
			auditCollectSecrets(auditJoinPath(path, key), child, secrets)
		}
	case []any:
		for index, child := range typed {
			auditCollectSecrets(fmt.Sprintf("%s[%d]", path, index), child, secrets)
		}
	case channelAuditUnparsed:
		if auditSensitivePath(path) && typed.raw != "" {
			secrets[typed.raw] = struct{}{}
		}
	}
}

func newChannelAuditRedactor(values ...any) channelAuditRedactor {
	secretSet := make(map[string]struct{})
	for _, value := range values {
		auditCollectSecrets("", value, secretSet)
	}
	secrets := make([]string, 0, len(secretSet))
	for secret := range secretSet {
		if len(secret) >= 4 {
			secrets = append(secrets, secret)
		}
	}
	sort.Slice(secrets, func(i, j int) bool {
		if len(secrets[i]) == len(secrets[j]) {
			return secrets[i] < secrets[j]
		}
		return len(secrets[i]) > len(secrets[j])
	})
	return channelAuditRedactor{secrets: secrets}
}

func (redactor channelAuditRedactor) redactKnownSecrets(value string) (string, bool) {
	redacted := false
	for _, secret := range redactor.secrets {
		if strings.Contains(value, secret) {
			value = strings.ReplaceAll(value, secret, channelAuditRedactedValue)
			redacted = true
		}
	}
	return value, redacted
}

func auditKeySummary(raw any) map[string]any {
	value, _ := raw.(string)
	trimmed := strings.TrimSpace(value)
	count := 0
	if trimmed != "" {
		if strings.HasPrefix(trimmed, "[") {
			if decoded, ok := auditDecodeJSON(trimmed); ok {
				if items, isArray := decoded.([]any); isArray {
					count = len(items)
				}
			}
		}
		if count == 0 {
			if strings.HasPrefix(trimmed, "{") {
				count = 1
			} else {
				for _, key := range strings.Split(trimmed, "\n") {
					if strings.TrimSpace(key) != "" {
						count++
					}
				}
			}
		}
	}
	return map[string]any{
		"configured": trimmed != "",
		"key_count":  count,
	}
}

func auditSanitizeURL(raw string, redactor channelAuditRedactor) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return channelAuditInvalidURLValue, true
	}
	sensitive := false
	if parsed.User != nil {
		parsed.User = nil
		sensitive = true
	}
	pathSegments := strings.Split(parsed.Path, "/")
	previousSegment := ""
	for index, segment := range pathSegments {
		if auditSensitiveURLPathSegment(previousSegment, segment) && segment != "" {
			pathSegments[index] = channelAuditRedactedValue
			sensitive = true
		}
		previousSegment = segment
	}
	parsed.Path = strings.Join(pathSegments, "/")
	parsed.RawPath = ""
	query := parsed.Query()
	for key, values := range query {
		if auditSensitiveURLQueryName(key) {
			for index := range values {
				values[index] = channelAuditRedactedValue
			}
			query[key] = values
			sensitive = true
			continue
		}
		for index, value := range values {
			values[index], _ = redactor.redactKnownSecrets(value)
		}
		query[key] = values
	}
	parsed.RawQuery = query.Encode()
	if parsed.Fragment != "" {
		parsed.Fragment = channelAuditRedactedValue
		sensitive = true
	}
	result, knownSecretRedacted := redactor.redactKnownSecrets(parsed.String())
	return result, sensitive || knownSecretRedacted
}

func auditSanitizeEmbeddedURLs(raw string, redactor channelAuditRedactor) (string, bool) {
	sensitive := false
	result := channelAuditURLPattern.ReplaceAllStringFunc(raw, func(candidate string) string {
		sanitized, candidateSensitive := auditSanitizeURL(candidate, redactor)
		sensitive = sensitive || candidateSensitive
		return sanitized
	})
	result, knownSecretRedacted := redactor.redactKnownSecrets(result)
	return result, sensitive || knownSecretRedacted
}

func auditSanitizeValue(path string, value any, redactor channelAuditRedactor) (any, bool) {
	if path == "key" {
		return auditKeySummary(value), true
	}
	if auditSensitivePath(path) {
		_, headerMap := value.(map[string]any)
		if strings.ToLower(path) != "header_override" || !headerMap {
			if value == nil || value == "" {
				return value, true
			}
			return channelAuditRedactedValue, true
		}
	}
	switch typed := value.(type) {
	case channelAuditUnparsed:
		return channelAuditUnparsedValue, true
	case channelAuditMissing:
		return nil, false
	case map[string]any:
		result := make(map[string]any, len(typed))
		sensitive := false
		for key, child := range typed {
			sanitized, childSensitive := auditSanitizeValue(auditJoinPath(path, key), child, redactor)
			result[key] = sanitized
			sensitive = sensitive || childSensitive
		}
		return result, sensitive
	case []any:
		result := make([]any, 0, len(typed))
		sensitive := false
		for index, child := range typed {
			sanitized, childSensitive := auditSanitizeValue(fmt.Sprintf("%s[%d]", path, index), child, redactor)
			result = append(result, sanitized)
			sensitive = sensitive || childSensitive
		}
		return result, sensitive
	case string:
		if auditURLPath(path) {
			return auditSanitizeURL(typed, redactor)
		}
		if strings.Contains(typed, "://") {
			return auditSanitizeEmbeddedURLs(typed, redactor)
		}
		return redactor.redactKnownSecrets(typed)
	case json.RawMessage:
		return json.RawMessage(append([]byte(nil), typed...)), false
	default:
		return value, false
	}
}

func auditBuildChanges(path string, before any, after any, redactor channelAuditRedactor, changes *[]ChannelAuditChange) {
	if reflect.DeepEqual(before, after) {
		return
	}
	beforeMap, beforeIsMap := before.(map[string]any)
	afterMap, afterIsMap := after.(map[string]any)
	if beforeIsMap || afterIsMap {
		if !beforeIsMap && before != nil {
			beforeValue, beforeSensitive := auditSanitizeValue(path, before, redactor)
			afterValue, afterSensitive := auditSanitizeValue(path, after, redactor)
			*changes = append(*changes, ChannelAuditChange{Field: path, Before: beforeValue, After: afterValue, Sensitive: beforeSensitive || afterSensitive})
			return
		}
		if !afterIsMap && after != nil {
			beforeValue, beforeSensitive := auditSanitizeValue(path, before, redactor)
			afterValue, afterSensitive := auditSanitizeValue(path, after, redactor)
			*changes = append(*changes, ChannelAuditChange{Field: path, Before: beforeValue, After: afterValue, Sensitive: beforeSensitive || afterSensitive})
			return
		}
		keys := make(map[string]struct{}, len(beforeMap)+len(afterMap))
		for key := range beforeMap {
			keys[key] = struct{}{}
		}
		for key := range afterMap {
			keys[key] = struct{}{}
		}
		orderedKeys := make([]string, 0, len(keys))
		for key := range keys {
			orderedKeys = append(orderedKeys, key)
		}
		sort.Strings(orderedKeys)
		changeStart := len(*changes)
		for _, key := range orderedKeys {
			beforeChild, beforeExists := beforeMap[key]
			if !beforeExists {
				beforeChild = channelAuditMissing{}
			}
			afterChild, afterExists := afterMap[key]
			if !afterExists {
				afterChild = channelAuditMissing{}
			}
			auditBuildChanges(auditJoinPath(path, key), beforeChild, afterChild, redactor, changes)
		}
		if len(*changes) == changeStart {
			beforeValue, beforeSensitive := auditSanitizeValue(path, before, redactor)
			afterValue, afterSensitive := auditSanitizeValue(path, after, redactor)
			*changes = append(*changes, ChannelAuditChange{Field: path, Before: beforeValue, After: afterValue, Sensitive: beforeSensitive || afterSensitive})
		}
		return
	}
	beforeValue, beforeSensitive := auditSanitizeValue(path, before, redactor)
	afterValue, afterSensitive := auditSanitizeValue(path, after, redactor)
	*changes = append(*changes, ChannelAuditChange{
		Field:     path,
		Before:    beforeValue,
		After:     afterValue,
		Sensitive: beforeSensitive || afterSensitive,
	})
}

func auditAction(pair ChannelAuditPair) string {
	if pair.Action != "" {
		return pair.Action
	}
	if pair.Before == nil && pair.After != nil {
		return ChannelAuditActionCreate
	}
	if pair.Before != nil && pair.After == nil {
		return ChannelAuditActionDelete
	}
	return ChannelAuditActionUpdate
}

func auditChannelIdentity(pair ChannelAuditPair) (id int, name string, channelType int) {
	channel := pair.After
	if channel == nil {
		channel = pair.Before
	}
	if channel == nil {
		return 0, "", 0
	}
	return channel.Id, channel.Name, channel.Type
}

// RecordChannelAuditPairs creates one immutable audit row per channel. Pairs
// with no effective configuration change are skipped. tx should be the same
// primary-database transaction used for the channel mutation.
func RecordChannelAuditPairs(tx *gorm.DB, actor ChannelAuditActor, source string, batchID string, pairs []ChannelAuditPair) error {
	if len(pairs) == 0 {
		return nil
	}
	if tx == nil {
		tx = DB
	}
	if tx == nil {
		return fmt.Errorf("channel audit database is not initialized")
	}
	if actor.Type == "" {
		if actor.ID > 0 {
			actor.Type = ChannelAuditActorAdmin
		} else {
			actor.Type = ChannelAuditActorSystem
		}
	}
	if actor.Name == "" && actor.Type == ChannelAuditActorSystem {
		actor.Name = "system"
	}

	createdAt := common.GetTimestamp()
	records := make([]ChannelAuditLog, 0, len(pairs))
	for _, pair := range pairs {
		if pair.Before == nil && pair.After == nil {
			continue
		}
		beforeRaw := channelAuditRawSnapshot(pair.Before)
		afterRaw := channelAuditRawSnapshot(pair.After)
		redactor := newChannelAuditRedactor(beforeRaw, afterRaw)
		changes := make([]ChannelAuditChange, 0)
		auditBuildChanges("", beforeRaw, afterRaw, redactor, &changes)
		if len(changes) == 0 {
			continue
		}

		var beforeSafe any
		if pair.Before != nil {
			beforeSafe, _ = auditSanitizeValue("", beforeRaw, redactor)
		}
		var afterSafe any
		if pair.After != nil {
			afterSafe, _ = auditSanitizeValue("", afterRaw, redactor)
		}
		beforeBytes, err := common.Marshal(beforeSafe)
		if err != nil {
			return fmt.Errorf("marshal channel audit before snapshot: %w", err)
		}
		afterBytes, err := common.Marshal(afterSafe)
		if err != nil {
			return fmt.Errorf("marshal channel audit after snapshot: %w", err)
		}
		changesBytes, err := common.Marshal(changes)
		if err != nil {
			return fmt.Errorf("marshal channel audit changes: %w", err)
		}
		changedFields := make([]string, 0, len(changes))
		for _, change := range changes {
			changedFields = append(changedFields, change.Field)
		}
		changedFieldsBytes, err := common.Marshal(changedFields)
		if err != nil {
			return fmt.Errorf("marshal channel audit changed fields: %w", err)
		}

		channelID, channelName, channelType := auditChannelIdentity(pair)
		records = append(records, ChannelAuditLog{
			ChannelId:       channelID,
			ChannelName:     channelName,
			ChannelType:     channelType,
			Action:          auditAction(pair),
			Source:          source,
			OperatorType:    actor.Type,
			OperatorId:      actor.ID,
			OperatorName:    actor.Name,
			OperatorRole:    actor.Role,
			RequestId:       actor.RequestID,
			Ip:              actor.IP,
			UserAgent:       actor.UserAgent,
			Method:          actor.Method,
			Path:            actor.Path,
			BatchId:         batchID,
			CreatedAt:       createdAt,
			BeforeSnapshot:  append([]byte(nil), beforeBytes...),
			AfterSnapshot:   append([]byte(nil), afterBytes...),
			Changes:         append([]byte(nil), changesBytes...),
			ChangedFields:   append([]byte(nil), changedFieldsBytes...),
			ChangeCount:     len(changes),
			SnapshotVersion: channelAuditSnapshotVersion,
		})
	}
	if len(records) == 0 {
		return nil
	}
	return tx.CreateInBatches(&records, 50).Error
}

// CreateChannelAuditLogs is kept as a descriptive alias for mutation callers.
func CreateChannelAuditLogs(tx *gorm.DB, actor ChannelAuditActor, source string, batchID string, pairs []ChannelAuditPair) error {
	return RecordChannelAuditPairs(tx, actor, source, batchID, pairs)
}

func GetChannelAuditLogs(startIdx int, num int, query ChannelAuditQuery) ([]*ChannelAuditLog, int64, error) {
	logs := make([]*ChannelAuditLog, 0)
	db := DB.Model(&ChannelAuditLog{})
	if query.ChannelID > 0 {
		db = db.Where("channel_id = ?", query.ChannelID)
	}
	if query.ChannelName != "" {
		db = db.Where("channel_name LIKE ?", "%"+query.ChannelName+"%")
	}
	if query.Action != "" {
		db = db.Where("action = ?", query.Action)
	}
	if query.Source != "" {
		db = db.Where("source = ?", query.Source)
	}
	if query.OperatorName != "" {
		db = db.Where("operator_name LIKE ?", "%"+query.OperatorName+"%")
	}
	if query.StartTime > 0 {
		db = db.Where("created_at >= ?", query.StartTime)
	}
	if query.EndTime > 0 {
		db = db.Where("created_at <= ?", query.EndTime)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := db.Select(
		"id", "channel_id", "channel_name", "channel_type", "action", "source",
		"operator_type", "operator_id", "operator_name", "operator_role", "request_id",
		"ip", "user_agent", "method", "path", "batch_id", "created_at", "changed_fields",
		"change_count", "snapshot_version",
	).Order("created_at desc, id desc").Offset(startIdx).Limit(num).Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

func GetChannelAuditLogByID(id int64) (*ChannelAuditLog, error) {
	log := &ChannelAuditLog{}
	if err := DB.First(log, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return log, nil
}
