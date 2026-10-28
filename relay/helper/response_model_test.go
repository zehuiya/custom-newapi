package helper

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStreamWritersRestoreResponseModel(t *testing.T) {
	for _, writer := range []struct {
		name, modelPath string
		write           func(*gin.Context) error
	}{
		{"chat raw", "model", func(c *gin.Context) error {
			return StringData(c, `{"object":"chat.completion.chunk","model":"upstream-C","choices":[{"delta":{"content":"answer"}}]}`)
		}},
		{"chat converted", "model", func(c *gin.Context) error {
			return ObjectData(c, map[string]any{"object": "chat.completion.chunk", "model": "upstream-C", "choices": []any{}})
		}},
		{"claude raw", "message.model", func(c *gin.Context) error {
			ClaudeChunkData(c, dto.ClaudeResponse{Type: "message_start"}, `{"type":"message_start","message":{"id":"msg-1","model":"upstream-C"},"extra":9007199254740993}`)
			return nil
		}},
		{"claude converted", "message.model", func(c *gin.Context) error {
			return ClaudeData(c, dto.ClaudeResponse{Type: "message_start", Message: &dto.ClaudeMediaMessage{Id: "msg-1", Model: "upstream-C"}})
		}},
		{"responses raw", "response.model", func(c *gin.Context) error {
			ResponseChunkData(c, dto.ResponsesStreamResponse{Type: "response.created"}, `{"type":"response.created","response":{"id":"resp-1","model":"upstream-C"},"extra":9007199254740993}`)
			return nil
		}},
		{"gemini native", "modelVersion", func(c *gin.Context) error {
			return StringData(c, `{"modelVersion":"upstream-C","candidates":[]}`)
		}},
	} {
		for _, enabled := range []bool{false, true} {
			t.Run(writer.name+" enabled="+strconv.FormatBool(enabled), func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{RestoreResponseModel: enabled})
				common.SetContextKey(c, constant.ContextKeyOriginalModel, "client-A")
				require.NoError(t, writer.write(c))
				require.NoError(t, StringData(c, "[DONE]"))
				var payloads []string
				for _, line := range strings.Split(w.Body.String(), "\n") {
					if strings.HasPrefix(line, "data: ") {
						payloads = append(payloads, strings.TrimPrefix(line, "data: "))
					}
				}
				require.Len(t, payloads, 2)
				model := "upstream-C"
				if enabled {
					model = "client-A"
				}
				require.Equal(t, model, gjson.Get(payloads[0], writer.modelPath).String())
				require.Equal(t, "[DONE]", payloads[1])
				if gjson.Get(payloads[0], "extra").Exists() {
					require.Equal(t, "9007199254740993", gjson.Get(payloads[0], "extra").Raw)
				}
			})
		}
	}
}
