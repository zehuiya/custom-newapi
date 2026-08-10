package claude

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDoRequestDeleteHeaderParamOverrideWinsOverClaudeHeaders(t *testing.T) {
	service.InitHttpClient()

	receivedHeaders := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(`{}`))
	ctx.Request.Header.Set("anthropic-beta", "unsupported-beta")
	ctx.Request.Header.Set("Content-Type", "application/json")

	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-header-delete-test",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: server.URL,
			ApiKey:         "test-key",
			HeadersOverride: map[string]interface{}{
				"*": "",
			},
			ParamOverride: map[string]interface{}{
				"operations": []interface{}{
					map[string]interface{}{
						"mode": "delete_header",
						"path": "Anthropic-Beta",
					},
				},
			},
		},
	}

	_, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{"model":"claude-header-delete-test"}`), info)
	require.NoError(t, err)
	require.Equal(t, []string{"anthropic-beta"}, info.RuntimeHeadersToDelete)

	response, err := (&Adaptor{}).DoRequest(ctx, info, bytes.NewBufferString(`{}`))
	require.NoError(t, err)
	defer response.(*http.Response).Body.Close()

	upstreamHeaders := <-receivedHeaders
	require.Empty(t, upstreamHeaders.Values("anthropic-beta"))
}
