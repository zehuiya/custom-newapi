package imagerouter

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
	openai.Adaptor
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info == nil {
		return "", errors.New("imagerouter adaptor: relay info is nil")
	}

	baseURL := strings.TrimRight(info.ChannelBaseUrl, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	switch info.RelayMode {
	case relayconstant.RelayModeImagesGenerations:
		return baseURL + "/v1/openai/images/generations", nil
	default:
		return relaycommon.GetFullRequestURL(baseURL, info.RequestURLPath, info.ChannelType), nil
	}
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	if info == nil {
		return errors.New("imagerouter adaptor: relay info is nil")
	}
	if info.ApiKey == "" {
		return errors.New("imagerouter adaptor: api key is required")
	}

	channel.SetupApiRequestHeader(info, c, req)
	req.Set("Authorization", "Bearer "+info.ApiKey)
	if req.Get("Content-Type") == "" {
		req.Set("Content-Type", "application/json")
	}
	if req.Get("Accept") == "" {
		req.Set("Accept", "application/json")
	}
	return nil
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	if info == nil {
		return nil, errors.New("imagerouter adaptor: relay info is nil")
	}
	if info.RelayMode != relayconstant.RelayModeImagesGenerations {
		return nil, errors.New("imagerouter adaptor: only image generations are supported")
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return nil, errors.New("imagerouter adaptor: prompt is required")
	}

	modelName := strings.TrimSpace(info.UpstreamModelName)
	if modelName == "" {
		modelName = strings.TrimSpace(request.Model)
	}
	if modelName == "" {
		modelName = strings.TrimSpace(info.OriginModelName)
	}
	modelName = upstreamModelName(modelName)
	request.Model = modelName
	info.UpstreamModelName = modelName

	if strings.TrimSpace(request.Quality) == "" {
		request.Quality = "auto"
	}
	if strings.TrimSpace(request.Size) == "" {
		request.Size = "auto"
	}
	one := uint(1)
	request.N = &one
	request.ResponseFormat = "b64_ephemeral"

	return request, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if info != nil && info.RelayMode == relayconstant.RelayModeImagesGenerations {
		return imageRouterImageHandler(c, resp, info)
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
