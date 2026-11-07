package relaypayloadlog

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
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
	response := newTestResponse(http.StatusOK, "application/json", "{}")
	body := response.Body
	CaptureUpstreamResponse(c, nil, response)
	require.Equal(t, body, response.Body)
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
	capture := value.(*requestCapture)
	require.Equal(t, len(requestBody), capture.entry.RequestRawBytes)
	sanitizedRequest, err := common.Marshal(capture.entry.Request)
	require.NoError(t, err)
	require.Contains(t, string(sanitizedRequest), "keep request text")
	require.Contains(t, string(sanitizedRequest), "media_content")
	require.NotContains(t, string(sanitizedRequest), base64Data)

	responseBody := `{"choices":[{"message":{"role":"assistant","reasoning_content":"keep reasoning","content":"keep response text","image_url":"data:image/png;base64,` + base64Data + `"},"finish_reason":"stop"}]}`
	response := newTestResponse(http.StatusOK, "application/json", responseBody)
	CaptureUpstreamResponse(c, nil, response)
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, responseBody, string(raw))
	_, err = c.Writer.WriteString("must not log downstream response")
	require.NoError(t, err)
	require.True(t, finish(nil))

	line := <-m.queue
	require.NotContains(t, string(line), base64Data)
	require.NotContains(t, string(line), "must not log downstream response")
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
	response := newTestResponse(http.StatusOK, "text/event-stream; charset=utf-8", streamBody)
	CaptureUpstreamResponse(c, nil, response)
	_, err := io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.True(t, finish(nil))

	line := <-m.queue
	require.NotContains(t, string(line), requestBase64)
	var record map[string]any
	require.NoError(t, common.Unmarshal(line, &record))
	require.Equal(t, true, record["upstream_stream"])
	require.Equal(t, false, record["stream"]) // Upstream SSE can be buffered for a non-streaming client.
	summary := record["response"].(string)
	require.Contains(t, summary, "reasoning_content: plan ")
	require.Contains(t, summary, "content: answer")
	require.Contains(t, summary, "tool_call:")
	require.Contains(t, summary, "search")
	require.Contains(t, summary, "usage:")
	require.Contains(t, summary, "stop_reason: tool_use")
	require.Contains(t, summary, "done: true")
}

func TestCaptureOriginalErrorsAcrossRetries(t *testing.T) {
	m := activateTestManager(t, Config{QueueSize: 2, MaxEntryBytes: 1024, SampleRate: 1})
	c := newTestContext()
	info := &relaycommon.RelayInfo{IsStream: true}
	finish := StartCapture(c, info, ProtocolOpenAI, func() ([]byte, error) {
		return []byte(`{"model":"gpt-test","input":"` + strings.Repeat("long input ", 150) + `"}`), nil
	})

	for i, body := range []string{"original plain error", `{"error":{"message":"original schema error","param":"tools.5.schema"}}`} {
		info.RetryIndex = i
		response := newTestResponse(http.StatusBadRequest, "application/json", body)
		response.Header.Set("X-Tt-Logid", "upstream-id")
		CaptureUpstreamResponse(c, info, response)
		got, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, body, string(got))
	}
	info.RetryIndex++ // A retry candidate without an HTTP response must not relabel the logged attempt.
	require.True(t, finish(&ErrorInfo{StatusCode: 429, Message: "mapped error"}))

	for i := range 2 {
		line := <-m.queue
		var record map[string]any
		require.NoError(t, common.Unmarshal(line, &record))
		require.Equal(t, "req-test", record["request_id"])
		require.Equal(t, float64(i), record["retry_index"])
		require.Equal(t, float64(400), record["upstream_status_code"])
		require.Equal(t, "upstream-id", record["upstream_request_id"])
		require.Equal(t, true, record["stream"])
		require.Equal(t, false, record["upstream_stream"])
		require.NotContains(t, record, "error")
		if i == 0 {
			require.Nil(t, record["request"])
			require.Equal(t, "original plain error", record["response"])
		} else {
			require.Equal(t, true, record["truncated"])
			require.Contains(t, string(line), "original schema error")
			require.Contains(t, string(line), "tools.5.schema")
		}
	}
}

func TestCaptureCapsBodyWithoutChangingReads(t *testing.T) {
	m := activateTestManager(t, Config{QueueSize: 1, MaxEntryBytes: 512, SampleRate: 1})
	c := newTestContext()
	finish := StartCapture(c, nil, ProtocolOpenAI, func() ([]byte, error) { return []byte("{}"), nil })
	text := `{"content":"` + strings.Repeat("answer ", 512) + `"}`
	response := newTestResponse(http.StatusOK, "application/json", text)
	CaptureUpstreamResponse(c, nil, response)
	body := response.Body.(*upstreamBody)
	require.Empty(t, body.buf)
	raw, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, text, string(raw))
	require.Len(t, body.buf, 512)
	require.NoError(t, body.Close())
	require.True(t, finish(nil))
	var record map[string]any
	require.NoError(t, common.Unmarshal(<-m.queue, &record))
	require.Equal(t, true, record["response_truncated"])
	require.Equal(t, float64(len(text)), record["response"].(map[string]any)["raw_bytes"])
}

func TestCaptureDoesNotSaveLocalResponseBody(t *testing.T) {
	m := activateTestManager(t, Config{QueueSize: 1, MaxEntryBytes: 1024, SampleRate: 1})
	c := newTestContext()
	finish := StartCapture(c, nil, ProtocolOpenAI, func() ([]byte, error) { return []byte("{}"), nil })
	c.String(http.StatusBadRequest, "local response only")
	require.True(t, finish(&ErrorInfo{StatusCode: 400, Message: "local metadata"}))
	var record map[string]any
	require.NoError(t, common.Unmarshal(<-m.queue, &record))
	require.Nil(t, record["response"])
	require.NotContains(t, record, "upstream_status_code")
	require.Equal(t, "local metadata", record["error"].(map[string]any)["message"])
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

func newTestResponse(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
