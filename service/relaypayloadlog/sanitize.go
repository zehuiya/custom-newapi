package relaypayloadlog

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
)

const maxLoggedStringLen = 16 << 10
const maxLoggedStreamTextLen = 1 << 20

var mediaContentTypes = map[string]bool{
	"image":       true,
	"input_image": true,
	"image_url":   true,
	"input_audio": true,
	"audio":       true,
	"video":       true,
	"video_url":   true,
}

func sanitizePayload(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	var value any
	if err := common.Unmarshal(data, &value); err == nil {
		return sanitizeValue(value, "")
	}
	return sanitizeString(string(data), "")
}

func sanitizeStreamChunks(chunks []string) any {
	summary := newStreamSummary()
	for _, chunk := range chunks {
		summary.addPayload("", chunk)
	}
	return summary.String()
}

func sanitizeStreamBody(data []byte) any {
	text := string(data)
	blocks := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n")
	summary := newStreamSummary()
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		event := ""
		dataLines := make([]string, 0, 1)
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
			switch {
			case strings.HasPrefix(line, "event:"):
				event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if len(dataLines) == 0 {
			summary.parseErrors++
			continue
		}
		payload := strings.Join(dataLines, "\n")
		summary.addPayload(event, payload)
	}
	return summary.String()
}

type streamSummary struct {
	reasoningContent strings.Builder
	content          strings.Builder
	toolCall         strings.Builder
	usage            string
	finishReason     string
	stopReason       string
	chunkCount       int
	parseErrors      int
	done             bool
}

func newStreamSummary() *streamSummary {
	return &streamSummary{}
}

func (s *streamSummary) addPayload(event string, payload string) {
	if s == nil {
		return
	}
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return
	}
	s.chunkCount++
	if payload == "[DONE]" {
		s.done = true
		return
	}

	var value any
	if err := common.UnmarshalJsonStr(payload, &value); err != nil {
		s.parseErrors++
		return
	}
	m, ok := value.(map[string]any)
	if !ok {
		return
	}
	s.addOpenAI(m)
	s.addAnthropic(event, m)
}

func (s *streamSummary) addOpenAI(m map[string]any) {
	if usage, ok := m["usage"]; ok && usage != nil {
		s.usage = stringifyStreamValue(usage, "usage")
	}
	choices, ok := sliceValue(m["choices"])
	if !ok {
		return
	}
	for _, choiceValue := range choices {
		choice, ok := choiceValue.(map[string]any)
		if !ok {
			continue
		}
		if finishReason, ok := stringMapValue(choice, "finish_reason"); ok && finishReason != "" {
			s.finishReason = finishReason
		}
		delta, ok := mapValue(choice["delta"])
		if !ok {
			continue
		}
		if reasoning, ok := stringMapValue(delta, "reasoning_content"); ok {
			s.reasoningContent.WriteString(reasoning)
		}
		if reasoning, ok := stringMapValue(delta, "reasoning"); ok {
			s.reasoningContent.WriteString(reasoning)
		}
		if content, ok := stringMapValue(delta, "content"); ok {
			s.content.WriteString(content)
		} else if contentValue, exists := delta["content"]; exists && contentValue != nil {
			appendStreamPart(&s.content, stringifyStreamValue(contentValue, "content"))
		}
		if toolCalls, ok := sliceValue(delta["tool_calls"]); ok {
			appendStreamPart(&s.toolCall, stringifyStreamValue(toolCalls, "tool_calls"))
		}
	}
}

func (s *streamSummary) addAnthropic(event string, m map[string]any) {
	typ, _ := stringMapValue(m, "type")
	if typ == "" {
		typ = event
	}
	if usage, ok := m["usage"]; ok && usage != nil {
		s.usage = stringifyStreamValue(usage, "usage")
	}
	switch typ {
	case "message_start":
		if message, ok := mapValue(m["message"]); ok {
			if usage, ok := message["usage"]; ok && usage != nil {
				s.usage = stringifyStreamValue(usage, "usage")
			}
		}
	case "content_block_start":
		s.addAnthropicContentBlockStart(m)
	case "content_block_delta":
		s.addAnthropicContentBlockDelta(m)
	case "message_delta":
		if delta, ok := mapValue(m["delta"]); ok {
			if stopReason, ok := stringMapValue(delta, "stop_reason"); ok && stopReason != "" {
				s.stopReason = stopReason
			}
		}
	case "message_stop":
		s.done = true
	}
	if stopReason, ok := stringMapValue(m, "stop_reason"); ok && stopReason != "" {
		s.stopReason = stopReason
	}
}

func (s *streamSummary) addAnthropicContentBlockStart(m map[string]any) {
	block, ok := mapValue(m["content_block"])
	if !ok {
		return
	}
	blockType, _ := stringMapValue(block, "type")
	switch blockType {
	case "thinking":
		if thinking, ok := stringMapValue(block, "thinking"); ok {
			s.reasoningContent.WriteString(thinking)
		}
	case "text":
		if text, ok := stringMapValue(block, "text"); ok {
			s.content.WriteString(text)
		}
	case "tool_use":
		appendStreamPart(&s.toolCall, stringifyStreamValue(block, "tool_call"))
	}
}

func (s *streamSummary) addAnthropicContentBlockDelta(m map[string]any) {
	delta, ok := mapValue(m["delta"])
	if !ok {
		return
	}
	deltaType, _ := stringMapValue(delta, "type")
	switch deltaType {
	case "thinking_delta":
		if thinking, ok := stringMapValue(delta, "thinking"); ok {
			s.reasoningContent.WriteString(thinking)
		}
	case "text_delta":
		if text, ok := stringMapValue(delta, "text"); ok {
			s.content.WriteString(text)
		}
	case "input_json_delta":
		if partial, ok := stringMapValue(delta, "partial_json"); ok && partial != "" {
			appendStreamPart(&s.toolCall, partial)
		}
	}
}

func (s *streamSummary) String() string {
	if s == nil {
		return ""
	}
	parts := make([]string, 0, 8)
	if reasoning := s.reasoningContent.String(); reasoning != "" {
		parts = append(parts, "reasoning_content: "+sanitizeStreamText(reasoning))
	}
	if content := s.content.String(); content != "" {
		parts = append(parts, "content: "+sanitizeStreamText(content))
	}
	if toolCall := s.toolCall.String(); toolCall != "" {
		parts = append(parts, "tool_call: "+sanitizeStreamText(toolCall))
	}
	if s.usage != "" {
		parts = append(parts, "usage: "+s.usage)
	}
	if s.finishReason != "" {
		parts = append(parts, "finish_reason: "+s.finishReason)
	}
	if s.stopReason != "" {
		parts = append(parts, "stop_reason: "+s.stopReason)
	}
	parts = append(parts, fmt.Sprintf("chunk_count: %d", s.chunkCount))
	parts = append(parts, fmt.Sprintf("done: %t", s.done))
	if s.parseErrors > 0 {
		parts = append(parts, fmt.Sprintf("parse_error_count: %d", s.parseErrors))
	}
	return strings.Join(parts, " | ")
}

func appendStreamPart(builder *strings.Builder, value string) {
	if builder == nil || value == "" {
		return
	}
	if builder.Len() > 0 {
		builder.WriteString(" ")
	}
	builder.WriteString(value)
}

func stringifyStreamValue(value any, key string) string {
	sanitized := sanitizeValue(value, key)
	if text, ok := sanitized.(string); ok {
		return sanitizeStreamText(text)
	}
	data, err := common.Marshal(sanitized)
	if err != nil {
		return sanitizeStreamText(fmt.Sprint(sanitized))
	}
	return sanitizeStreamText(string(data))
}

func sanitizeStreamText(value string) string {
	if value == "" {
		return value
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "data:") {
		return summarizeString(value, "omitted_data_uri")
	}
	if looksLikeBase64(value) {
		return summarizeString(value, "omitted_base64")
	}
	if len(value) > maxLoggedStreamTextLen {
		return value[:maxLoggedStreamTextLen] + fmt.Sprintf("...[truncated length=%d]", len(value))
	}
	return value
}

func sanitizeValue(value any, key string) any {
	switch v := value.(type) {
	case map[string]any:
		if typ, ok := stringMapValue(v, "type"); ok && mediaContentTypes[strings.ToLower(typ)] {
			return summarizeMediaMap(v, typ)
		}
		out := make(map[string]any, len(v))
		for childKey, childValue := range v {
			if shouldOmitField(childKey) {
				out[childKey] = summarizeValue(childValue, "omitted_media_or_base64")
				continue
			}
			out[childKey] = sanitizeValue(childValue, childKey)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, sanitizeValue(item, key))
		}
		return out
	case string:
		return sanitizeString(v, key)
	default:
		return value
	}
}

func sanitizeString(value string, key string) any {
	if value == "" {
		return value
	}
	lowerKey := normalizeKey(key)
	if isMediaKey(lowerKey) || isBase64Key(lowerKey) {
		return summarizeString(value, "omitted_media_or_base64")
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "data:") {
		return summarizeString(value, "omitted_data_uri")
	}
	if looksLikeBase64(value) {
		return summarizeString(value, "omitted_base64")
	}
	if len(value) > maxLoggedStringLen {
		return value[:maxLoggedStringLen] + fmt.Sprintf("...[truncated length=%d]", len(value))
	}
	return value
}

func summarizeMediaMap(m map[string]any, typ string) map[string]any {
	out := map[string]any{
		"type":    typ,
		"omitted": "media_content",
	}
	if text, ok := stringMapValue(m, "text"); ok && text != "" {
		out["text"] = sanitizeString(text, "text")
	}
	if name, ok := stringMapValue(m, "name"); ok && name != "" {
		out["name"] = name
	}
	if id, ok := stringMapValue(m, "id"); ok && id != "" {
		out["id"] = id
	}
	return out
}

func shouldOmitField(key string) bool {
	normalized := normalizeKey(key)
	return isBase64Key(normalized) || isMediaKey(normalized)
}

func isMediaKey(normalized string) bool {
	switch normalized {
	case "imageurl", "videourl", "inputaudio", "source", "inlineData", "inlinedata":
		return true
	case "imagebase64", "imageurls", "audiourl", "audiourls", "videourls":
		return true
	}
	return false
}

func isBase64Key(normalized string) bool {
	if strings.Contains(normalized, "base64") || strings.Contains(normalized, "b64") {
		return true
	}
	switch normalized {
	case "filedata", "binarydata", "binarydatabase64", "bytesbase64encoded":
		return true
	}
	return false
}

func summarizeValue(value any, reason string) any {
	switch v := value.(type) {
	case string:
		return summarizeString(v, reason)
	case []any:
		return fmt.Sprintf("[%s array_items=%d]", reason, len(v))
	case map[string]any:
		return fmt.Sprintf("[%s object_keys=%d]", reason, len(v))
	default:
		return fmt.Sprintf("[%s]", reason)
	}
}

func summarizeString(value string, reason string) string {
	return fmt.Sprintf("[%s length=%d]", reason, len(value))
}

func summarizePayload(data []byte, label string) any {
	return map[string]any{
		"omitted":    label,
		"raw_bytes":  len(data),
		"truncated":  true,
		"parse_hint": "entry_exceeded_max_size",
	}
}

func summarizeChunks(chunks []string) []any {
	out := make([]any, 0, len(chunks))
	for i, chunk := range chunks {
		out = append(out, map[string]any{
			"index":     i,
			"raw_bytes": len(chunk),
			"omitted":   "chunk_entry_exceeded_max_size",
		})
	}
	return out
}

func stringMapValue(m map[string]any, key string) (string, bool) {
	value, ok := m[key]
	if !ok {
		return "", false
	}
	str, ok := value.(string)
	return str, ok
}

func mapValue(value any) (map[string]any, bool) {
	m, ok := value.(map[string]any)
	return m, ok
}

func sliceValue(value any) ([]any, bool) {
	s, ok := value.([]any)
	return s, ok
}

func normalizeKey(key string) string {
	key = strings.ToLower(key)
	key = strings.ReplaceAll(key, "_", "")
	key = strings.ReplaceAll(key, "-", "")
	return key
}

func looksLikeBase64(value string) bool {
	if len(value) < 512 {
		return false
	}
	clean := strings.TrimSpace(value)
	if len(clean) < 512 || strings.ContainsAny(clean, " \n\r\t") {
		return false
	}
	valid := 0
	for _, r := range clean {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '+' || r == '/' || r == '=' || r == '-' || r == '_' {
			valid++
		}
	}
	return float64(valid)/float64(len(clean)) > 0.98
}
