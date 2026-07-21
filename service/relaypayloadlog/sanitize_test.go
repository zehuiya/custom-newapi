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
