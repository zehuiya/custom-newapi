package runware

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

type runwareImageResponse struct {
	Data   []runwareImageData  `json:"data"`
	Errors []runwareAPIError   `json:"errors,omitempty"`
	Error  *runwareSingleError `json:"error,omitempty"`
}

type runwareImageData struct {
	TaskType        string  `json:"taskType,omitempty"`
	TaskUUID        string  `json:"taskUUID,omitempty"`
	ImageUUID       string  `json:"imageUUID,omitempty"`
	ImageURL        string  `json:"imageURL,omitempty"`
	ImageBase64Data string  `json:"imageBase64Data,omitempty"`
	ImageDataURI    string  `json:"imageDataURI,omitempty"`
	NSFWContent     *bool   `json:"NSFWContent,omitempty"`
	Cost            float64 `json:"cost,omitempty"`
}

type runwareAPIError struct {
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	Parameter string `json:"parameter,omitempty"`
	Type      string `json:"type,omitempty"`
	TaskType  string `json:"taskType,omitempty"`
}

type runwareSingleError struct {
	Message string `json:"message,omitempty"`
}

type openAIImageResponse struct {
	Created int64             `json:"created"`
	Data    []openAIImageData `json:"data"`
	Usage   *dto.Usage        `json:"usage,omitempty"`
}

type openAIImageData struct {
	B64JSON string `json:"b64_json,omitempty"`
}

func runwareImageHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	if resp == nil {
		return nil, types.NewError(errors.New("runware adaptor: empty response"), types.ErrorCodeBadResponse)
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	service.CloseResponseBodyGracefully(resp)

	var imageResp runwareImageResponse
	if err := common.Unmarshal(responseBody, &imageResp); err != nil {
		return nil, types.NewOpenAIError(fmt.Errorf("runware adaptor: failed to decode response: %w", err), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if len(imageResp.Errors) > 0 {
		return nil, types.NewOpenAIError(errors.New(formatRunwareErrors(imageResp.Errors)), types.ErrorCodeBadResponse, resp.StatusCode)
	}
	if imageResp.Error != nil {
		message := strings.TrimSpace(imageResp.Error.Message)
		if message == "" {
			message = "runware adaptor: upstream returned an error"
		}
		return nil, types.NewOpenAIError(errors.New(message), types.ErrorCodeBadResponse, resp.StatusCode)
	}

	payload := openAIImageResponse{
		Created: common.GetTimestamp(),
		Data:    make([]openAIImageData, 0, len(imageResp.Data)),
	}

	for _, item := range imageResp.Data {
		b64 := strings.TrimSpace(item.ImageBase64Data)
		if b64 == "" {
			b64 = base64FromDataURI(item.ImageDataURI)
		}
		if b64 == "" && strings.TrimSpace(item.ImageURL) != "" {
			_, downloaded, err := service.GetImageFromUrl(item.ImageURL)
			if err != nil {
				return nil, types.NewOpenAIError(fmt.Errorf("runware adaptor: failed to download generated image: %w", err), types.ErrorCodeBadResponse, http.StatusInternalServerError)
			}
			b64 = downloaded
		}
		if b64 == "" {
			continue
		}
		payload.Data = append(payload.Data, openAIImageData{B64JSON: b64})
	}

	if len(payload.Data) == 0 {
		return nil, types.NewOpenAIError(errors.New("runware adaptor: no usable image data"), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if info != nil {
		info.PriceData.AddOtherRatio("n", float64(len(payload.Data)))
	}
	payload.Usage = channel.BuildImageResponseUsage()

	responseBytes, err := common.Marshal(payload)
	if err != nil {
		return nil, types.NewOpenAIError(fmt.Errorf("runware adaptor: failed to encode response: %w", err), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, resp, responseBytes)

	return payload.Usage, nil
}

func formatRunwareErrors(errs []runwareAPIError) string {
	messages := make([]string, 0, len(errs))
	for _, item := range errs {
		message := strings.TrimSpace(item.Message)
		if message == "" {
			message = strings.TrimSpace(item.Code)
		}
		if message == "" {
			continue
		}
		if item.Parameter != "" {
			message = fmt.Sprintf("%s (%s)", message, item.Parameter)
		}
		messages = append(messages, message)
	}
	if len(messages) == 0 {
		return "runware adaptor: upstream returned an error"
	}
	return strings.Join(messages, "; ")
}

func base64FromDataURI(dataURI string) string {
	dataURI = strings.TrimSpace(dataURI)
	if dataURI == "" {
		return ""
	}
	const marker = ";base64,"
	index := strings.Index(dataURI, marker)
	if index == -1 {
		return dataURI
	}
	return strings.TrimSpace(dataURI[index+len(marker):])
}
