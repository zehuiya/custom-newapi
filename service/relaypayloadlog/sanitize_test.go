package relaypayloadlog

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestSanitizePayloadOmitsMediaAndKeepsText(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-test",
		"messages":[{
			"role":"user",
			"content":[
				{"type":"text","text":"keep this text"},
				{"type":"image_url","image_url":{"url":"data:image/png;base64,` + strings.Repeat("a", 600) + `"}}
			]
		}],
		"metadata":{"note":"also keep"}
	}`)

	sanitized := sanitizePayload(raw)
	root, ok := sanitized.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "gpt-test", root["model"])

	messages := root["messages"].([]any)
	message := messages[0].(map[string]any)
	content := message["content"].([]any)
	require.Equal(t, "keep this text", content[0].(map[string]any)["text"])
	require.Equal(t, "image_url", content[1].(map[string]any)["type"])
	require.Equal(t, "media_content", content[1].(map[string]any)["omitted"])
}

func TestSanitizePayloadRedactsEmbeddedDataURI(t *testing.T) {
	raw := []byte(`{"error":{"message":"decode failed for identifier[base64:data:image/png;base64,` + strings.Repeat("a", 600) + `] after upload"}}`)

	sanitized := sanitizePayload(raw).(map[string]any)
	message := sanitized["error"].(map[string]any)["message"].(string)
	require.Contains(t, message, "decode failed for identifier[base64:")
	require.Contains(t, message, "omitted_data_uri")
	require.Contains(t, message, "after upload")
	require.NotContains(t, message, "data:image/png")
	require.NotContains(t, message, strings.Repeat("a", 512))
}

func TestRedactEmbeddedDataURIsPreservesCaseInsensitiveBehavior(t *testing.T) {
	first := "DATA:image/png;base64," + strings.Repeat("a", 600)
	second := "data:video/mp4;base64," + strings.Repeat("b", 600)
	value := "before " + first + " middle " + second + " after"

	redacted, changed := redactEmbeddedDataURIs(value)

	require.True(t, changed)
	require.Equal(t,
		"before "+summarizeString(first, "omitted_data_uri")+
			" middle "+summarizeString(second, "omitted_data_uri")+" after",
		redacted,
	)
}

func TestRedactEmbeddedDataURIsReturnsOriginalStringWhenUnchanged(t *testing.T) {
	value := strings.Repeat("ordinary text ", 100)
	redacted, changed := redactEmbeddedDataURIs(value)
	require.False(t, changed)
	require.Equal(t, value, redacted)
}

func TestRedactEmbeddedDataURIsHandlesManyValues(t *testing.T) {
	values := make([]string, 12)
	for i := range values {
		values[i] = "DATA:image/png;base64," + strings.Repeat("a", 32)
	}
	redacted, changed := redactEmbeddedDataURIs(strings.Join(values, " "))
	require.True(t, changed)
	require.Equal(t, len(values), strings.Count(redacted, "[omitted_data_uri length="))
	require.NotContains(t, redacted, "DATA:image")
}

func TestNormalizeKeyPreservesExistingNormalization(t *testing.T) {
	require.Equal(t, "imageurl", normalizeKey("Image_URL"))
	require.Equal(t, "reasoningcontent", normalizeKey("REASONING-CONTENT"))
	require.Equal(t, "äimage", normalizeKey("Ä_IMAGE"))
}

func TestSanitizeStreamChunksKeepsReasoningAndToolCalls(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"reasoning_content":"think "}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"text","content":"hello "}}]}`,
		`{"choices":[{"delta":{"content":"world","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"search","arguments":"{\"q\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"hello\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"completion_tokens":10}}`,
		`[DONE]`,
	}

	sanitized := sanitizeStreamChunks(chunks).(string)
	require.Contains(t, sanitized, "reasoning_content: think text")
	require.Contains(t, sanitized, "content: hello world")
	require.Contains(t, sanitized, "tool_call:")
	require.Contains(t, sanitized, "search")
	require.Contains(t, sanitized, "usage:")
	require.Contains(t, sanitized, `"completion_tokens":10`)
	require.Contains(t, sanitized, "finish_reason: tool_calls")
	require.Contains(t, sanitized, "chunk_count: 5")
	require.Contains(t, sanitized, "done: true")
}

func TestSanitizeStreamChunksRedactsToolCallDataURI(t *testing.T) {
	arguments := `{"image":"data:image/png;base64,` + strings.Repeat("a", 600) + `"}`
	chunk, err := common.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"delta": map[string]any{
				"tool_calls": []any{map[string]any{
					"function": map[string]any{"name": "inspect", "arguments": arguments},
				}},
			},
		}},
	})
	require.NoError(t, err)

	sanitized := sanitizeStreamChunks([]string{string(chunk)}).(string)
	require.Contains(t, sanitized, "tool_call:")
	require.Contains(t, sanitized, "omitted_data_uri")
	require.NotContains(t, sanitized, "data:image/png")
	require.NotContains(t, sanitized, strings.Repeat("a", 512))
}

func TestSanitizeStreamBodyParsesSSEAndOmitsMedia(t *testing.T) {
	body := []byte("event: content_block_delta\n" +
		`data: {"delta":{"type":"thinking_delta","thinking":"keep thinking"}}` + "\n\n" +
		"data: {\"type\":\"image\",\"source\":{\"data\":\"data:image/png;base64," + strings.Repeat("a", 600) + "\"}}\n\n" +
		"data: [DONE]\n\n")

	sanitized := sanitizeStreamBody(body).(string)
	require.Contains(t, sanitized, "reasoning_content: keep thinking")
	require.Contains(t, sanitized, "chunk_count: 3")
	require.Contains(t, sanitized, "done: true")
	require.NotContains(t, sanitized, "chunks")
	require.NotContains(t, sanitized, "data:image/png")
}

func TestSanitizeStreamBodyCompactsAnthropicToolUse(t *testing.T) {
	body := []byte(
		"event: content_block_start\n" +
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"plan "}}` + "\n\n" +
			"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"more"}}` + "\n\n" +
			"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}` + "\n\n" +
			"event: content_block_start\n" +
			`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}` + "\n\n" +
			"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Guangzhou\"}"}}` + "\n\n" +
			"event: message_delta\n" +
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}` + "\n\n" +
			"event: message_stop\n" +
			`data: {"type":"message_stop"}` + "\n\n")

	sanitized := sanitizeStreamBody(body).(string)
	require.Contains(t, sanitized, "reasoning_content: plan more")
	require.Contains(t, sanitized, "content: answer")
	require.Contains(t, sanitized, "tool_call:")
	require.Contains(t, sanitized, "toolu_1")
	require.Contains(t, sanitized, "get_weather")
	require.Contains(t, sanitized, `{"city":"Guangzhou"}`)
	require.Contains(t, sanitized, "usage:")
	require.Contains(t, sanitized, `"output_tokens":12`)
	require.Contains(t, sanitized, "stop_reason: tool_use")
	require.Contains(t, sanitized, "chunk_count: 7")
	require.Contains(t, sanitized, "done: true")
}

func TestSanitizeStreamBodyCompactsOpenAIResponses(t *testing.T) {
	body := []byte(
		"event: response.created\n" +
			`data: {"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}` + "\n\n" +
			"event: response.reasoning_summary_text.delta\n" +
			`data: {"type":"response.reasoning_summary_text.delta","delta":"plan "}` + "\n\n" +
			"event: response.reasoning_summary_text.delta\n" +
			`data: {"type":"response.reasoning_summary_text.delta","delta":"carefully"}` + "\n\n" +
			"event: response.output_text.delta\n" +
			`data: {"type":"response.output_text.delta","delta":"hello "}` + "\n\n" +
			"event: response.output_text.delta\n" +
			`data: {"type":"response.output_text.delta","delta":"world"}` + "\n\n" +
			"event: response.output_item.added\n" +
			`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"search","arguments":""}}` + "\n\n" +
			"event: response.function_call_arguments.delta\n" +
			`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"q\":\"test\"}"}` + "\n\n" +
			"event: response.completed\n" +
			`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":12,"output_tokens":8,"total_tokens":20}}}` + "\n\n")

	sanitized := sanitizeStreamBody(body).(string)
	require.Contains(t, sanitized, "reasoning_content: plan carefully")
	require.Contains(t, sanitized, "content: hello world")
	require.Contains(t, sanitized, "tool_call:")
	require.Contains(t, sanitized, "call_1")
	require.Contains(t, sanitized, "search")
	require.Contains(t, sanitized, `{"q":"test"}`)
	require.Contains(t, sanitized, "usage:")
	require.Contains(t, sanitized, `"input_tokens":12`)
	require.Contains(t, sanitized, "response_status: completed")
	require.Contains(t, sanitized, "chunk_count: 8")
	require.Contains(t, sanitized, "done: true")
}

func TestSanitizeStreamBodyUsesCompletedResponsesPayloadAsFallback(t *testing.T) {
	body := []byte("event: response.completed\n" +
		`data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"fallback reasoning"}]},{"type":"message","content":[{"type":"output_text","text":"fallback answer"}]},{"type":"function_call","call_id":"call_2","name":"lookup","arguments":"{\"id\":2}"}],"usage":{"input_tokens":4,"output_tokens":6,"total_tokens":10}}}` + "\n\n")

	sanitized := sanitizeStreamBody(body).(string)
	require.Contains(t, sanitized, "reasoning_content: fallback reasoning")
	require.Contains(t, sanitized, "content: fallback answer")
	require.Contains(t, sanitized, "tool_call:")
	require.Contains(t, sanitized, "call_2")
	require.Contains(t, sanitized, "lookup")
	require.Contains(t, sanitized, "response_status: completed")
	require.Contains(t, sanitized, "done: true")
}

func TestNormalizeConfigClampsSampleRate(t *testing.T) {
	cfg := Config{SampleRate: 2}
	normalizeConfig(&cfg)
	require.Equal(t, 1.0, cfg.SampleRate)
	require.Equal(t, 8, cfg.WriterWorkers)

	cfg = Config{SampleRate: -0.1}
	normalizeConfig(&cfg)
	require.Equal(t, 0.0, cfg.SampleRate)
}

func TestShouldSampleHonorsZeroAndOne(t *testing.T) {
	require.False(t, (&manager{cfg: Config{SampleRate: 0}}).shouldSample())
	require.True(t, (&manager{cfg: Config{SampleRate: 1}}).shouldSample())
}

var benchmarkSanitizedText string
var benchmarkNormalizedKey string

func BenchmarkNormalizeKey(b *testing.B) {
	keys := []string{"content", "image_url", "REASONING-CONTENT", "input_audio"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchmarkNormalizedKey = normalizeKey(keys[i%len(keys)])
	}
}

func BenchmarkRedactEmbeddedDataURIs(b *testing.B) {
	media := "DATA:image/png;base64," + strings.Repeat("a", 4096)
	parts := make([]string, 0, 17)
	for i := 0; i < 8; i++ {
		parts = append(parts, strings.Repeat("ordinary text ", 512), media)
	}
	value := strings.Join(parts, " ")
	b.ReportAllocs()
	b.SetBytes(int64(len(value)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkSanitizedText, _ = redactEmbeddedDataURIs(value)
	}
}
