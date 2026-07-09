package relaypayloadlog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestWriteEntryIncludesErrorInfo(t *testing.T) {
	dir := t.TempDir()
	m := &manager{
		cfg: Config{
			Dir:           dir,
			MaxFileBytes:  1 << 20,
			MaxEntryBytes: 1 << 20,
		},
	}
	worker := &writerWorker{id: 0, manager: m}

	worker.writeEntry(rawEntry{
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
		RequestRaw:  []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}`),
		ResponseRaw: []byte(`{"error":{"message":"bad request","type":"new_api_error"}}`),
		Error: &ErrorInfo{
			StatusCode: 400,
			Type:       "new_api_error",
			Code:       "upstream_error",
			Message:    "bad request",
		},
	})
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
