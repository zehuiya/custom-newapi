package openai

import (
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func OaiResponsesCompactionHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	var compactResp dto.OpenAIResponsesCompactionResponse
	if err := common.Unmarshal(responseBody, &compactResp); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := compactResp.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	usage := dto.Usage{}
	if compactResp.Usage != nil {
		fillRelayUsageFromResponsesUsage(&usage, compactResp.Usage)
		if service.NormalizeNoCacheUsageForRelay(c, info, &usage) {
			syncResponsesUsageFieldsFromRelayUsage(compactResp.Usage, &usage)
			if modifiedBody, changed := rewriteResponsesUsagePayload(responseBody, false, &usage); changed {
				responseBody = modifiedBody
			}
		}
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)

	markOpenAIUsageSemantic(&usage)
	return &usage, nil
}
