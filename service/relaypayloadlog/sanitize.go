package relaypayloadlog

import (
	"fmt"
	"strconv"
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
	responseStatus   string
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
	s.addOpenAIResponses(m)
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

func (s *streamSummary) addOpenAIResponses(m map[string]any) {
	typ, _ := stringMapValue(m, "type")
	if !strings.HasPrefix(typ, "response.") {
		return
	}

	switch typ {
	case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		if delta, ok := stringMapValue(m, "delta"); ok {
			s.reasoningContent.WriteString(delta)
		}
	case "response.reasoning_text.done", "response.reasoning_summary_text.done":
		if s.reasoningContent.Len() == 0 {
			if text, ok := stringMapValue(m, "text"); ok {
				s.reasoningContent.WriteString(text)
			}
		}
	case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		if s.reasoningContent.Len() == 0 {
			s.appendResponsesReasoningPart(m["part"])
		}
	case "response.output_text.delta":
		if delta, ok := stringMapValue(m, "delta"); ok {
			s.content.WriteString(delta)
		}
	case "response.output_text.done":
		if s.content.Len() == 0 {
			if text, ok := stringMapValue(m, "text"); ok {
				s.content.WriteString(text)
			}
		}
	case "response.output_item.added":
		s.appendResponsesToolItem(m["item"])
	case "response.output_item.done":
		if s.toolCall.Len() == 0 {
			s.appendResponsesToolItem(m["item"])
		}
	case "response.function_call_arguments.delta":
		if delta, ok := stringMapValue(m, "delta"); ok {
			s.toolCall.WriteString(delta)
		}
	case "response.function_call_arguments.done":
		if s.toolCall.Len() == 0 {
			if arguments, ok := stringMapValue(m, "arguments"); ok {
				s.toolCall.WriteString(arguments)
			}
		}
	case "response.completed", "response.incomplete", "response.failed", "response.error":
		s.done = true
		s.addOpenAIResponsesFinal(m)
	}
}

func (s *streamSummary) appendResponsesReasoningPart(value any) {
	part, ok := mapValue(value)
	if !ok {
		return
	}
	if text, ok := stringMapValue(part, "text"); ok {
		s.reasoningContent.WriteString(text)
	}
}

func (s *streamSummary) appendResponsesToolItem(value any) {
	item, ok := mapValue(value)
	if !ok {
		return
	}
	itemType, _ := stringMapValue(item, "type")
	if itemType != "function_call" {
		return
	}
	tool := make(map[string]any, 4)
	for _, key := range []string{"id", "call_id", "name", "arguments"} {
		if field, exists := item[key]; exists && field != nil && field != "" {
			tool[key] = field
		}
	}
	if len(tool) > 0 {
		appendStreamPart(&s.toolCall, stringifyStreamValue(tool, "tool_call"))
	}
}

func (s *streamSummary) addOpenAIResponsesFinal(m map[string]any) {
	response, ok := mapValue(m["response"])
	if !ok {
		return
	}
	if status, ok := stringMapValue(response, "status"); ok {
		s.responseStatus = status
	}
	if usage, exists := response["usage"]; exists && usage != nil {
		s.usage = stringifyStreamValue(usage, "usage")
	}
	outputs, ok := sliceValue(response["output"])
	if !ok {
		return
	}
	for _, outputValue := range outputs {
		output, ok := mapValue(outputValue)
		if !ok {
			continue
		}
		outputType, _ := stringMapValue(output, "type")
		switch outputType {
		case "message":
			if s.content.Len() == 0 {
				s.appendResponsesOutputContent(output["content"])
			}
		case "reasoning":
			if s.reasoningContent.Len() == 0 {
				s.appendResponsesReasoningSummary(output["summary"])
			}
		case "function_call":
			if s.toolCall.Len() == 0 {
				s.appendResponsesToolItem(output)
			}
		}
	}
}

func (s *streamSummary) appendResponsesOutputContent(value any) {
	content, ok := sliceValue(value)
	if !ok {
		return
	}
	for _, partValue := range content {
		part, ok := mapValue(partValue)
		if !ok {
			continue
		}
		if text, ok := stringMapValue(part, "text"); ok {
			s.content.WriteString(text)
		}
	}
}

func (s *streamSummary) appendResponsesReasoningSummary(value any) {
	parts, ok := sliceValue(value)
	if !ok {
		return
	}
	for _, part := range parts {
		s.appendResponsesReasoningPart(part)
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
	if s.responseStatus != "" {
		parts = append(parts, "response_status: "+s.responseStatus)
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
	if hasDataURIPrefix(value) {
		return summarizeString(value, "omitted_data_uri")
	}
	if redacted, changed := redactEmbeddedDataURIs(value); changed {
		value = redacted
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
		if typ, ok := stringMapValue(v, "type"); ok && isMediaContentType(typ) {
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
	if hasDataURIPrefix(value) {
		return summarizeString(value, "omitted_data_uri")
	}
	if redacted, changed := redactEmbeddedDataURIs(value); changed {
		value = redacted
	}
	if looksLikeBase64(value) {
		return summarizeString(value, "omitted_base64")
	}
	if len(value) > maxLoggedStringLen {
		return value[:maxLoggedStringLen] + fmt.Sprintf("...[truncated length=%d]", len(value))
	}
	return value
}

func redactEmbeddedDataURIs(value string) (string, bool) {
	start := indexDataURIPrefix(value, 0)
	if start < 0 {
		return value, false
	}

	type dataURIRange struct {
		start int
		end   int
	}
	var inlineRanges [8]dataURIRange
	ranges := inlineRanges[:0]
	redactedLength := len(value)
	for start >= 0 {
		end := dataURIEnd(value, start)
		ranges = append(ranges, dataURIRange{start: start, end: end})
		redactedLength -= end - start
		redactedLength += len("[omitted_data_uri length=]") + decimalDigitCount(end-start)
		start = indexDataURIPrefix(value, end)
	}

	var redacted strings.Builder
	redacted.Grow(redactedLength)
	cursor := 0
	for _, dataURI := range ranges {
		redacted.WriteString(value[cursor:dataURI.start])
		redacted.WriteString("[omitted_data_uri length=")
		var lengthBuffer [20]byte
		redacted.Write(strconv.AppendInt(lengthBuffer[:0], int64(dataURI.end-dataURI.start), 10))
		redacted.WriteByte(']')
		cursor = dataURI.end
	}
	redacted.WriteString(value[cursor:])
	return redacted.String(), true
}

func dataURIEnd(value string, start int) int {
	for i := start + len("data:"); i < len(value); i++ {
		switch value[i] {
		case ' ', '\t', '\r', '\n', '"', '\'', ']', ')', '}':
			return i
		}
	}
	return len(value)
}

func decimalDigitCount(value int) int {
	digits := 1
	for value >= 10 {
		value /= 10
		digits++
	}
	return digits
}

func hasDataURIPrefix(value string) bool {
	trimmed := strings.TrimSpace(value)
	return len(trimmed) >= len("data:") && isDataURIPrefixAt(trimmed, 0)
}

func indexDataURIPrefix(value string, from int) int {
	if from < 0 {
		from = 0
	}
	for search := from; search < len(value); {
		relativeColon := strings.IndexByte(value[search:], ':')
		if relativeColon < 0 {
			return -1
		}
		colon := search + relativeColon
		start := colon - len("data")
		if start >= from && isDataURIPrefixAt(value, start) {
			return start
		}
		search = colon + 1
	}
	return -1
}

func isDataURIPrefixAt(value string, start int) bool {
	if start < 0 || start+len("data:") > len(value) {
		return false
	}
	return lowerASCII(value[start]) == 'd' &&
		lowerASCII(value[start+1]) == 'a' &&
		lowerASCII(value[start+2]) == 't' &&
		lowerASCII(value[start+3]) == 'a' &&
		value[start+4] == ':'
}

func lowerASCII(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}

func isMediaContentType(value string) bool {
	if mediaContentTypes[value] {
		return true
	}
	switch len(value) {
	case len("image"):
		return strings.EqualFold(value, "image") ||
			strings.EqualFold(value, "audio") ||
			strings.EqualFold(value, "video")
	case len("image_url"):
		return strings.EqualFold(value, "image_url") ||
			strings.EqualFold(value, "video_url")
	case len("input_image"):
		return strings.EqualFold(value, "input_image") ||
			strings.EqualFold(value, "input_audio")
	default:
		return false
	}
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

func summarizePayloadBytes(rawBytes int, label string) any {
	return map[string]any{
		"omitted":    label,
		"raw_bytes":  rawBytes,
		"truncated":  true,
		"parse_hint": "entry_exceeded_max_size",
	}
}

func truncateMetadata(value string) string {
	const maxMetadataBytes = 256
	if len(value) <= maxMetadataBytes {
		return value
	}
	end := maxMetadataBytes
	for end > 0 && value[end]&0xc0 == 0x80 {
		end--
	}
	return value[:end] + "...[truncated]"
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
	needsNormalization := false
	for i := 0; i < len(key); i++ {
		char := key[i]
		if char >= 0x80 {
			key = strings.ToLower(key)
			key = strings.ReplaceAll(key, "_", "")
			return strings.ReplaceAll(key, "-", "")
		}
		if char == '_' || char == '-' || (char >= 'A' && char <= 'Z') {
			needsNormalization = true
		}
	}
	if !needsNormalization {
		return key
	}

	var normalized strings.Builder
	normalized.Grow(len(key))
	for i := 0; i < len(key); i++ {
		char := key[i]
		if char == '_' || char == '-' {
			continue
		}
		normalized.WriteByte(lowerASCII(char))
	}
	return normalized.String()
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
