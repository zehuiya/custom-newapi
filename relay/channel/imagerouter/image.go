package imagerouter

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

type imageRouterImageResponse struct {
	Created int64                  `json:"created"`
	Data    []imageRouterImageData `json:"data"`
	Error   any                    `json:"error,omitempty"`
}

type imageRouterImageData struct {
	URL           string  `json:"url,omitempty"`
	B64JSON       string  `json:"b64_json,omitempty"`
	RevisedPrompt *string `json:"revised_prompt,omitempty"`
}

type openAIImageResponse struct {
	Created int64           `json:"created"`
	Data    openAIImageData `json:"data"`
	Usage   *dto.Usage      `json:"usage,omitempty"`
}

type openAIImageData struct {
	B64JSON       string  `json:"b64_json,omitempty"`
	RevisedPrompt *string `json:"revised_prompt,omitempty"`
}

func imageRouterImageHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	if resp == nil {
		return nil, types.NewError(errors.New("imagerouter adaptor: empty response"), types.ErrorCodeBadResponse)
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	service.CloseResponseBodyGracefully(resp)

	var imageResp imageRouterImageResponse
	if err := common.Unmarshal(responseBody, &imageResp); err != nil {
		return nil, types.NewOpenAIError(fmt.Errorf("imagerouter adaptor: failed to decode response: %w", err), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if imageResp.Error != nil {
		if openAIError := dto.GetOpenAIError(imageResp.Error); openAIError != nil {
			return nil, types.WithOpenAIError(*openAIError, resp.StatusCode)
		}
		return nil, types.NewOpenAIError(errors.New("imagerouter adaptor: upstream returned an error"), types.ErrorCodeBadResponse, resp.StatusCode)
	}

	payload := openAIImageResponse{
		Created: imageResp.Created,
	}
	if payload.Created == 0 {
		payload.Created = common.GetTimestamp()
	}

	hasImage := false
	for _, item := range imageResp.Data {
		b64 := strings.TrimSpace(item.B64JSON)
		if b64 == "" && strings.TrimSpace(item.URL) != "" {
			_, downloaded, err := service.GetImageFromUrl(item.URL)
			if err != nil {
				return nil, types.NewOpenAIError(fmt.Errorf("imagerouter adaptor: failed to download generated image: %w", err), types.ErrorCodeBadResponse, http.StatusInternalServerError)
			}
			b64 = downloaded
		}
		if b64 == "" {
			continue
		}
		payload.Data = openAIImageData{
			B64JSON:       b64,
			RevisedPrompt: item.RevisedPrompt,
		}
		hasImage = true
		break
	}

	if !hasImage {
		return nil, types.NewOpenAIError(errors.New("imagerouter adaptor: no usable image data"), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if info != nil {
		info.PriceData.AddOtherRatio("n", 1)
	}
	payload.Usage = channel.BuildImageResponseUsage()

	responseBytes, err := common.Marshal(payload)
	if err != nil {
		return nil, types.NewOpenAIError(fmt.Errorf("imagerouter adaptor: failed to encode response: %w", err), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, resp, responseBytes)

	return payload.Usage, nil
}
