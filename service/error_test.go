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
			name:             "normalize an error carried by status code 200",
			statusCode:       200,
			statusCodeConfig: `{"200":503}`,
			expectedCode:     400,
		},
		{
			name:         "normalize without mapping",
			statusCode:   500,
			expectedCode: 400,
		},
		{
			name:             "normalize with empty mapping",
			statusCode:       403,
			statusCodeConfig: `{}`,
			expectedCode:     400,
		},
		{
			name:             "normalize with invalid mapping",
			statusCode:       502,
			statusCodeConfig: `{`,
			expectedCode:     400,
		},
		{
			name:             "normalize before mapping string value",
			statusCode:       500,
			statusCodeConfig: `{"400":"429","500":"503"}`,
			expectedCode:     429,
		},
		{
			name:             "normalize before mapping int value",
			statusCode:       401,
			statusCodeConfig: `{"400":429}`,
			expectedCode:     429,
		},
		{
			name:             "normalize existing 400 before mapping",
			statusCode:       400,
			statusCodeConfig: `{"400":429}`,
			expectedCode:     429,
		},
		{
			name:             "mapping applies only once",
			statusCode:       503,
			statusCodeConfig: `{"400":429,"429":503}`,
			expectedCode:     429,
		},
		{
			name:             "invalid normalized mapping value",
			statusCode:       503,
			statusCodeConfig: `{"400":"bad-code"}`,
			expectedCode:     400,
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
				Err:        errors.New("original upstream error"),
			}
			ResetStatusCode(newAPIError, tc.statusCodeConfig)
			require.Equal(t, tc.expectedCode, newAPIError.StatusCode)
			if tc.statusCode == http.StatusTooManyRequests {
				require.Equal(t, "original upstream error", newAPIError.Error())
			} else {
				require.Equal(t, "无效参数", newAPIError.Error())
			}
		})
	}
}

func TestResetStatusCodeErrorFormats(t *testing.T) {
	t.Parallel()

	factories := map[string]func(int) *types.NewAPIError{
		"openai": func(status int) *types.NewAPIError {
			return types.WithOpenAIError(types.OpenAIError{
				Message: "original upstream error", Type: "upstream_error", Code: "upstream_code",
			}, status, types.ErrOptionWithSkipRetry())
		},
		"claude": func(status int) *types.NewAPIError {
			return types.WithClaudeError(types.ClaudeError{
				Message: "original upstream error", Type: "upstream_error",
			}, status, types.ErrOptionWithSkipRetry())
		},
		"generic": func(status int) *types.NewAPIError {
			return types.NewError(errors.New("original upstream error"), types.ErrorCodeBadResponse,
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
				require.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
				message := "无效参数"
				if status == http.StatusTooManyRequests {
					message = "original upstream error"
				}
				require.Equal(t, message, apiErr.Error())
				require.Equal(t, message, apiErr.ToOpenAIError().Message)
				require.Equal(t, message, apiErr.ToClaudeError().Message)
				require.Equal(t, originalCode, apiErr.GetErrorCode())
				require.True(t, types.IsSkipRetryError(apiErr))
			}
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
			resp := &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(body))}
			apiErr := RelayErrorHandler(context.Background(), resp, false)
			ResetStatusCode(apiErr, `{"400":429,"500":503}`)
			require.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
			require.Equal(t, "无效参数", apiErr.ToOpenAIError().Message)
			require.Equal(t, "无效参数", apiErr.ToClaudeError().Message)
		})
	}
}

func TestResetStatusCodeNil(t *testing.T) {
	require.NotPanics(t, func() { ResetStatusCode(nil, `{"400":429}`) })
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
		`{"error":{"message":"original bad request","code":"InvalidParameter"}}`,
		"plain upstream error",
	} {
		response := &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(body)),
		}
		apiErr := RelayErrorHandler(context.Background(), response, false)
		ResetStatusCode(apiErr, `{"400":429}`)
		require.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
		require.Equal(t, "无效参数", apiErr.ToOpenAIError().Message)
	}
	require.Empty(t, output.String())
}
