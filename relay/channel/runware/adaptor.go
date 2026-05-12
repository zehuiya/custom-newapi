package runware

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Adaptor struct {
	openai.Adaptor
}

type imageInferenceTask struct {
	TaskType       string `json:"taskType"`
	TaskUUID       string `json:"taskUUID"`
	Model          string `json:"model"`
	PositivePrompt string `json:"positivePrompt"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	NumberResults  int    `json:"numberResults"`
	OutputType     string `json:"outputType"`
	OutputFormat   string `json:"outputFormat"`
	DeliveryMethod string `json:"deliveryMethod,omitempty"`
	OutputQuality  *int   `json:"outputQuality,omitempty"`
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info == nil {
		return "", errors.New("runware adaptor: relay info is nil")
	}

	baseURL := strings.TrimRight(info.ChannelBaseUrl, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	switch info.RelayMode {
	case relayconstant.RelayModeImagesGenerations:
		return baseURL + "/v1", nil
	default:
		return relaycommon.GetFullRequestURL(baseURL, info.RequestURLPath, info.ChannelType), nil
	}
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	if info == nil {
		return errors.New("runware adaptor: relay info is nil")
	}
	if info.ApiKey == "" {
		return errors.New("runware adaptor: api key is required")
	}

	channel.SetupApiRequestHeader(info, c, req)
	req.Set("Authorization", "Bearer "+info.ApiKey)
	req.Set("Content-Type", "application/json")
	req.Set("Accept", "application/json")
	return nil
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	if info == nil {
		return nil, errors.New("runware adaptor: relay info is nil")
	}
	if info.RelayMode != relayconstant.RelayModeImagesGenerations {
		return nil, errors.New("runware adaptor: only image generations are supported")
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return nil, errors.New("runware adaptor: prompt is required")
	}

	modelName := strings.TrimSpace(info.UpstreamModelName)
	if modelName == "" {
		modelName = strings.TrimSpace(request.Model)
	}
	if modelName == "" {
		modelName = strings.TrimSpace(info.OriginModelName)
	}
	modelName = upstreamModelName(modelName)
	info.UpstreamModelName = modelName

	width, height, err := parseImageSize(request.Size)
	if err != nil {
		return nil, err
	}
	task := imageInferenceTask{
		TaskType:       "imageInference",
		TaskUUID:       uuid.New().String(),
		Model:          modelName,
		PositivePrompt: request.Prompt,
		Width:          width,
		Height:         height,
		NumberResults:  1,
		OutputType:     "base64Data",
		OutputFormat:   parseOutputFormat(request.OutputFormat),
		DeliveryMethod: "sync",
	}
	if outputQuality := outputQualityForRequest(request.Quality); outputQuality != 0 {
		task.OutputQuality = &outputQuality
	}

	return []imageInferenceTask{task}, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if info != nil && info.RelayMode == relayconstant.RelayModeImagesGenerations {
		return runwareImageHandler(c, resp, info)
	}
	return a.Adaptor.DoResponse(c, resp, info)
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}

func upstreamModelName(modelName string) string {
	switch strings.TrimSpace(modelName) {
	case "", ModelQwenImage2512:
		return UpstreamModelQwenImage2512
	default:
		return modelName
	}
}

func parseImageSize(size string) (int, int, error) {
	size = strings.ToLower(strings.TrimSpace(size))
	if size == "" || size == "auto" {
		return 1024, 1024, nil
	}

	parts := strings.Split(size, "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("runware adaptor: invalid image size %q", size)
	}
	width, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("runware adaptor: invalid image width %q", parts[0])
	}
	height, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("runware adaptor: invalid image height %q", parts[1])
	}
	if !validRunwareDimension(width) || !validRunwareDimension(height) {
		return 0, 0, fmt.Errorf("runware adaptor: image size must be between 256 and 2048 pixels and divisible by 16")
	}
	return width, height, nil
}

func validRunwareDimension(value int) bool {
	return value >= 256 && value <= 2048 && value%16 == 0
}

func parseOutputFormat(raw []byte) string {
	outputFormat := "WEBP"
	if len(raw) == 0 {
		return outputFormat
	}

	var requested string
	if err := common.Unmarshal(raw, &requested); err != nil {
		return outputFormat
	}

	switch strings.ToUpper(strings.TrimSpace(requested)) {
	case "JPG", "JPEG":
		return "JPG"
	case "PNG":
		return "PNG"
	case "WEBP":
		return "WEBP"
	default:
		return outputFormat
	}
}

func outputQualityForRequest(quality string) int {
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case "low":
		return 80
	case "high", "hd":
		return 99
	default:
		return 95
	}
}
