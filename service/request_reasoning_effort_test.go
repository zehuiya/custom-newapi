package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCaptureRequestReasoningEffortAcrossProtocols(t *testing.T) {
	for _, endpoint := range []struct{ path, prefix, suffix string }{
		{"/v1/chat/completions", `{"reasoning_effort":`, `}`},
		{"/pg/chat/completions", `{"reasoning_effort":`, `}`},
		{"/v1/responses", `{"reasoning":{"effort":`, `}}`},
		{"/v1/messages", `{"output_config":{"effort":`, `}}`},
	} {
		for _, value := range []string{`"high"`, `"none"`, `"max"`, `"future-v3"`, `null`, `""`, `false`, `7`, `{}`, `[]`} {
			t.Run(endpoint.path+"/"+value, func(t *testing.T) {
				body := endpoint.prefix + value + endpoint.suffix
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, endpoint.path+"?beta=true", strings.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				t.Cleanup(func() { common.CleanupBodyStorage(c) })
				require.NoError(t, CaptureRequestReasoningEffort(c))
				effort := GetRequestReasoningEffort(c)
				require.NotNil(t, effort)
				require.True(t, effort.Present)
				expectedValue := ""
				if strings.HasPrefix(value, `"`) {
					expectedValue = strings.Trim(value, `"`)
				}
				require.Equal(t, expectedValue, effort.Value)
				preserved, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				require.Equal(t, body, string(preserved))
				// A changed retry body cannot change the original routing state.
				c.Request.Body = io.NopCloser(strings.NewReader(`{}`))
				require.NoError(t, CaptureRequestReasoningEffort(c))
				require.Same(t, effort, GetRequestReasoningEffort(c))
			})
		}
	}
	for _, test := range []struct {
		path, body string
		present    bool
	}{
		{"/v1/chat/completions", `{}`, false},
		{"/v1/responses", `{"reasoning":{"summary":"auto"}}`, false},
		{"/v1/messages", `{"thinking":{"type":"adaptive"},"output_config":{"format":{}}}`, false},
		{"/v1/responses", `{"reasoning":null}`, true},
		{"/v1/messages", `{"output_config":"high"}`, true},
		{"/v1/responses", `{"reasoning_effort":"high"}`, false},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		c.Request.Header.Set("Content-Type", "application/json")
		require.NoError(t, CaptureRequestReasoningEffort(c))
		require.Equal(t, &dto.RequestReasoningEffort{Present: test.present}, GetRequestReasoningEffort(c), "%s %s", test.path, test.body)
		common.CleanupBodyStorage(c)
	}
}
