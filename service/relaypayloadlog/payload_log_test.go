package relaypayloadlog

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestStartCaptureDoesNotReadUnsampledRequest(t *testing.T) {
	m := activateTestManager(t, Config{
		QueueSize:     1,
		MaxEntryBytes: 1 << 20,
		SampleRate:    0,
	})
	c := newTestContext()
	loadCount := 0

	finish := StartCapture(c, nil, ProtocolOpenAI, func() ([]byte, error) {
		loadCount++
		return []byte(`{"model":"gpt-test"}`), nil
	})

	require.Nil(t, finish)
	require.Zero(t, loadCount)
	require.Empty(t, m.queue)
	_, exists := c.Get(requestContextKey)
	require.False(t, exists)
}

func TestStartCaptureSanitizesBeforeQueue(t *testing.T) {
	m := activateTestManager(t, Config{
		QueueSize:     2,
		MaxEntryBytes: 1 << 20,
		SampleRate:    1,
	})
	c := newTestContext()
	base64Data := strings.Repeat("a", 4096)
	requestBody := []byte(`{
		"model":"gpt-test",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"keep request text"},
			{"type":"image_url","image_url":{"url":"data:image/png;base64,` + base64Data + `"}}
		]}]
	}`)

	finish := StartCapture(c, nil, ProtocolOpenAI, func() ([]byte, error) {
		return requestBody, nil
	})
	require.NotNil(t, finish)
	require.Empty(t, m.queue)

	value, exists := c.Get(requestContextKey)
	require.True(t, exists)
	capture := value.(requestCapture)
	require.Equal(t, len(requestBody), capture.RawBytes)
	sanitizedRequest, err := common.Marshal(capture.Payload)
	require.NoError(t, err)
	require.Contains(t, string(sanitizedRequest), "keep request text")
	require.Contains(t, string(sanitizedRequest), "media_content")
	require.NotContains(t, string(sanitizedRequest), base64Data)

	responseBody := `{"choices":[{"message":{"role":"assistant","reasoning_content":"keep reasoning","content":"keep response text","image_url":"data:image/png;base64,` + base64Data + `"},"finish_reason":"stop"}]}`
	_, err = c.Writer.Write([]byte(responseBody))
	require.NoError(t, err)
	require.True(t, finish(false, nil))

	line := <-m.queue
	require.NotContains(t, string(line), base64Data)
	require.Contains(t, string(line), "omitted_media_or_base64")
	var record map[string]any
	require.NoError(t, common.Unmarshal(line, &record))
	require.Equal(t, "keep reasoning", record["response"].(map[string]any)["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["reasoning_content"])
	require.Equal(t, "keep response text", record["response"].(map[string]any)["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["content"])
}

func TestStartCaptureSanitizesAndCompactsStreamBeforeQueue(t *testing.T) {
	m := activateTestManager(t, Config{
		QueueSize:     2,
		MaxEntryBytes: 1 << 20,
		SampleRate:    1,
	})
	c := newTestContext()
	requestBase64 := strings.Repeat("b", 4096)
	requestBody := []byte(`{"model":"claude-test","messages":[{"role":"user","content":[{"type":"text","text":"keep"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + requestBase64 + `"}}]}]}`)
	finish := StartCapture(c, nil, ProtocolAnthropic, func() ([]byte, error) {
		return requestBody, nil
	})
	require.NotNil(t, finish)

	streamBody := "event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"plan "}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"answer"}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"search","input":{"q":"test"}}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"
	_, err := c.Writer.Write([]byte(streamBody))
	require.NoError(t, err)
	require.True(t, finish(true, nil))

	line := <-m.queue
	require.NotContains(t, string(line), requestBase64)
	var record map[string]any
	require.NoError(t, common.Unmarshal(line, &record))
	response := record["response"].(string)
	require.Contains(t, response, "reasoning_content: plan ")
	require.Contains(t, response, "content: answer")
	require.Contains(t, response, "tool_call:")
	require.Contains(t, response, "search")
	require.Contains(t, response, "usage:")
	require.Contains(t, response, "stop_reason: tool_use")
	require.Contains(t, response, "done: true")
}

func TestPrepareLineTruncatesBeforeQueue(t *testing.T) {
	m := &manager{cfg: Config{MaxEntryBytes: 512}}
	line, err := m.prepareLine(capturedEntry{
		Meta: entryMeta{
			CreatedAt: time.Unix(1, 0).UTC(),
			RequestID: "req-large",
			Protocol:  ProtocolOpenAI,
		},
		Request:          map[string]any{"content": strings.Repeat("request", 1000)},
		RequestRawBytes:  7000,
		Response:         map[string]any{"content": strings.Repeat("response", 1000)},
		ResponseRawBytes: 8000,
	})

	require.NoError(t, err)
	require.LessOrEqual(t, len(line), 512)
	var record map[string]any
	require.NoError(t, common.Unmarshal(line, &record))
	require.Equal(t, true, record["truncated"])
	require.Equal(t, float64(7000), record["request"].(map[string]any)["raw_bytes"])
	require.Equal(t, float64(8000), record["response"].(map[string]any)["raw_bytes"])
}

func TestPreparedEntryIncludesErrorInfo(t *testing.T) {
	dir := t.TempDir()
	m := &manager{
		cfg: Config{
			Dir:           dir,
			MaxFileBytes:  1 << 20,
			MaxEntryBytes: 1 << 20,
		},
	}
	line, err := m.prepareLine(capturedEntry{
		Meta: entryMeta{
			CreatedAt:   time.Unix(1, 0).UTC(),
			RequestID:   "req-error",
			Protocol:    ProtocolOpenAI,
			Path:        "/v1/chat/completions",
			Method:      "POST",
			Model:       "gpt-test",
			ChannelID:   123,
			ChannelName: "error-channel",
		},
		Request:  sanitizePayload([]byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}`)),
		Response: sanitizePayload([]byte(`{"error":{"message":"bad request","type":"new_api_error"}}`)),
		Error: &ErrorInfo{
			StatusCode: 400,
			Type:       "new_api_error",
			Code:       "upstream_error",
			Message:    "bad request",
		},
	})
	require.NoError(t, err)

	worker := &writerWorker{id: 0, manager: m}
	worker.writeEntry(line)
	worker.closeFile()

	files, err := filepath.Glob(filepath.Join(dir, "payload-*.jsonl"))
	require.NoError(t, err)
	require.Len(t, files, 1)

	data, err := os.ReadFile(files[0])
	require.NoError(t, err)
	var record map[string]any
	require.NoError(t, common.Unmarshal(bytes.TrimSpace(data), &record))
	require.Equal(t, "req-error", record["request_id"])
	require.Equal(t, "gpt-test", record["request"].(map[string]any)["model"])
	require.Equal(t, "bad request", record["response"].(map[string]any)["error"].(map[string]any)["message"])

	errorInfo := record["error"].(map[string]any)
	require.Equal(t, float64(400), errorInfo["status_code"])
	require.Equal(t, "new_api_error", errorInfo["type"])
	require.Equal(t, "upstream_error", errorInfo["code"])
	require.Equal(t, "bad request", errorInfo["message"])
}

func activateTestManager(t *testing.T, cfg Config) *manager {
	t.Helper()
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1
	}
	m := &manager{
		cfg:   cfg,
		queue: make(chan []byte, cfg.QueueSize),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	activeMu.Lock()
	previous := active
	active = m
	activeMu.Unlock()
	t.Cleanup(func() {
		activeMu.Lock()
		active = previous
		activeMu.Unlock()
	})
	return m
}

func newTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "req-test")
	return c
}
