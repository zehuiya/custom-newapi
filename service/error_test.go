package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResetStatusCode(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		statusCode       int
		statusCodeConfig string
		expectedCode     int
		normalizeMessage bool
	}{
		{
			name:             "map string value",
			statusCode:       429,
			statusCodeConfig: `{"429":"503"}`,
			expectedCode:     503,
		},
		{
			name:             "map int value",
			statusCode:       429,
			statusCodeConfig: `{"429":503}`,
			expectedCode:     503,
		},
		{
			name:             "skip invalid string value",
			statusCode:       429,
			statusCodeConfig: `{"429":"bad-code"}`,
			expectedCode:     429,
		},
		{
			name:             "do not normalize an error carried by status code 200",
			statusCode:       200,
			statusCodeConfig: `{"400":429}`,
			expectedCode:     200,
		},
		{
			name:             "do not normalize below the 4xx range",
			statusCode:       399,
			statusCodeConfig: `{"400":429}`,
			expectedCode:     399,
		},
		{
			name:             "normalize without mapping",
			statusCode:       499,
			expectedCode:     400,
			normalizeMessage: true,
		},
		{
			name:             "normalize with empty mapping",
			statusCode:       403,
			statusCodeConfig: `{}`,
			expectedCode:     400,
			normalizeMessage: true,
		},
		{
			name:             "normalize with invalid mapping",
			statusCode:       422,
			statusCodeConfig: `{`,
			expectedCode:     400,
			normalizeMessage: true,
		},
		{
			name:             "server error uses its own mapping",
			statusCode:       500,
			statusCodeConfig: `{"400":"429","500":"503"}`,
			expectedCode:     503,
		},
		{
			name:             "server error does not match the 400 mapping",
			statusCode:       502,
			statusCodeConfig: `{"400":429}`,
			expectedCode:     502,
		},
		{
			name:             "mapping a server error to 400 does not normalize its message",
			statusCode:       502,
			statusCodeConfig: `{"502":400}`,
			expectedCode:     400,
		},
		{
			name:             "normalize before mapping int value",
			statusCode:       401,
			statusCodeConfig: `{"400":429,"401":503}`,
			expectedCode:     429,
			normalizeMessage: true,
		},
		{
			name:             "normalize existing 400 before mapping",
			statusCode:       400,
			statusCodeConfig: `{"400":429}`,
			expectedCode:     429,
			normalizeMessage: true,
		},
		{
			name:             "mapping applies only once",
			statusCode:       403,
			statusCodeConfig: `{"400":429,"429":503}`,
			expectedCode:     429,
			normalizeMessage: true,
		},
		{
			name:             "invalid normalized mapping value",
			statusCode:       404,
			statusCodeConfig: `{"400":"bad-code"}`,
			expectedCode:     400,
			normalizeMessage: true,
		},
		{
			name:         "preserve original 429",
			statusCode:   429,
			expectedCode: 429,
		},
		{
			name:             "preserve 429 message after mapping to 400",
			statusCode:       429,
			statusCodeConfig: `{"429":400}`,
			expectedCode:     400,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			newAPIError := &types.NewAPIError{
				StatusCode: tc.statusCode,
				Err:        errors.New("original upstream error, rEqUeSt_id: hidden"),
			}
			ResetStatusCode(newAPIError, tc.statusCodeConfig)
			require.Equal(t, tc.expectedCode, newAPIError.StatusCode)
			if tc.normalizeMessage {
				require.Equal(t, "original upstream error, ", newAPIError.Error())
			} else {
				require.Equal(t, "original upstream error, rEqUeSt_id: hidden", newAPIError.Error())
			}
		})
	}
}

func TestResetStatusCodeErrorFormats(t *testing.T) {
	t.Parallel()

	factories := map[string]func(int) *types.NewAPIError{
		"openai": func(status int) *types.NewAPIError {
			return types.WithOpenAIError(types.OpenAIError{
				Message: "上游错误, REQUEST_id: hidden", Type: "upstream_error", Code: "upstream_code",
			}, status, types.ErrOptionWithSkipRetry())
		},
		"claude": func(status int) *types.NewAPIError {
			return types.WithClaudeError(types.ClaudeError{
				Message: "上游错误, REQUEST_id: hidden", Type: "upstream_error",
			}, status, types.ErrOptionWithSkipRetry())
		},
		"generic": func(status int) *types.NewAPIError {
			return types.NewError(errors.New("上游错误, REQUEST_id: hidden"), types.ErrorCodeBadResponse,
				types.ErrOptionWithStatusCode(status), types.ErrOptionWithSkipRetry())
		},
	}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for status := 400; status < 600; status++ {
				apiErr := factory(status)
				originalCode := apiErr.GetErrorCode()
				ResetStatusCode(apiErr, `{"400":429}`)
				expectedStatus := status
				message := "上游错误, REQUEST_id: hidden"
				if status < http.StatusInternalServerError && status != http.StatusTooManyRequests {
					if status != http.StatusRequestEntityTooLarge {
						expectedStatus = http.StatusTooManyRequests
					}
					message = "上游错误, "
				}
				require.Equal(t, expectedStatus, apiErr.StatusCode, "upstream status %d", status)
				require.Equal(t, message, apiErr.Error())
				require.Equal(t, message, apiErr.ToOpenAIError().Message)
				require.Equal(t, message, apiErr.ToClaudeError().Message)
				require.Equal(t, originalCode, apiErr.GetErrorCode())
				require.True(t, types.IsSkipRetryError(apiErr))
			}
		})
	}
}

func TestResetStatusCodeResponseMessage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ original, want string }{
		{"limit 1M token, request_id:abc", "limit 1M token, "},
		{"İ限额; ReQuEsT:abc REQUEST:def", "İ限额; "},
		{"Request ID abc", ""},
		{"limit 1M token", "limit 1M token"},
		{"  invalid temperature  ", "  invalid temperature  "},
	} {
		t.Run(tc.original, func(t *testing.T) {
			apiErr := types.WithOpenAIError(types.OpenAIError{Message: tc.original, Code: "limit", Param: "input"}, http.StatusForbidden)
			ResetStatusCode(apiErr, `{"400":429}`)
			// The relay adds its own request ID to the internal message just before serialization.
			apiErr.SetMessage(common.MessageWithRequestId(apiErr.Error(), "gateway-id"))
			require.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
			require.Equal(t, tc.want, apiErr.ToOpenAIError().Message)
			require.Equal(t, tc.want, apiErr.ToClaudeError().Message)
			require.Equal(t, "limit", apiErr.ToOpenAIError().Code)
			require.Equal(t, "input", apiErr.ToOpenAIError().Param)
		})
	}
}

func TestRelayErrorHandlerOverrideBeforeMapping(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"openai": `{"error":{"message":"upstream failure","type":"server_error","code":"internal"}}`,
		"claude": `{"type":"error","error":{"type":"api_error","message":"upstream failure"}}`,
		"gemini": `{"error":{"code":500,"message":"upstream failure","status":"INTERNAL"}}`,
		"plain":  `upstream failure`,
		"empty":  ``,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			clientResp := &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(body))}
			clientErr := RelayErrorHandler(context.Background(), clientResp, false)
			original := clientErr.Error()
			ResetStatusCode(clientErr, `{"400":429}`)
			require.Equal(t, http.StatusTooManyRequests, clientErr.StatusCode)
			require.Equal(t, original, clientErr.ToOpenAIError().Message)
			require.Equal(t, original, clientErr.ToClaudeError().Message)

			serverResp := &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader(body))}
			serverErr := RelayErrorHandler(context.Background(), serverResp, false)
			openAIMessage := serverErr.ToOpenAIError().Message
			claudeMessage := serverErr.ToClaudeError().Message
			ResetStatusCode(serverErr, `{"400":429}`)
			require.Equal(t, http.StatusBadGateway, serverErr.StatusCode)
			require.Equal(t, openAIMessage, serverErr.ToOpenAIError().Message)
			require.Equal(t, claudeMessage, serverErr.ToClaudeError().Message)
		})
	}
}

func TestResetStatusCodeNil(t *testing.T) {
	require.NotPanics(t, func() { ResetStatusCode(nil, `{"400":429}`) })
}

func TestResetStatusCodePreservesTooBigErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantText   string
	}{
		{"openai", 400, `{"error":{"message":"Entry too big, request_id: upstream-id"}}`, 400, "Entry too big, "},
		{"original 413", 413, `{"error":{"message":"Entry TOO BIG"}}`, 413, "Entry TOO BIG"},
		{"413 without phrase", 413, `{"error":{"message":"Payload exceeds the limit"}}`, 413, "Payload exceeds the limit"},
		{"phrase after request", 413, `{"error":{"message":"Invalid request: payload too big"}}`, 413, "Invalid "},
		{"plain text", 400, "Entry Too Big; internal raw details", 400, "bad response status code 400"},
		{"original 429", 429, `{"error":{"message":"Entry too big"}}`, 429, "Entry too big"},
		{"other errors still map", 400, `{"error":{"message":"Entry invalid"}}`, 429, "Entry invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}
			apiErr := RelayErrorHandler(context.Background(), resp, false)
			for range 2 {
				ResetStatusCode(apiErr, `{"400":429,"413":429}`)
				require.Equal(t, tc.wantStatus, apiErr.StatusCode)
				require.Equal(t, tc.wantText, apiErr.ToOpenAIError().Message)
				require.Equal(t, tc.wantText, apiErr.ToClaudeError().Message)
			}
		})
	}
}

func TestRelayErrorHandlerDoesNotPrintBadRequestBody(t *testing.T) {
	var output bytes.Buffer
	common.LogWriterMu.Lock()
	previous := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &output
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = previous
		common.LogWriterMu.Unlock()
	})

	for _, body := range []string{
		`{"error":{"message":"original invalid input","code":"InvalidParameter"}}`,
		"plain upstream error",
	} {
		response := &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(body)),
		}
		apiErr := RelayErrorHandler(context.Background(), response, false)
		original := apiErr.Error()
		ResetStatusCode(apiErr, `{"400":429}`)
		require.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
		require.Equal(t, original, apiErr.ToOpenAIError().Message)
	}
	require.Empty(t, output.String())
}
