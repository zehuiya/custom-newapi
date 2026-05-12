package imagerouter

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetRequestURLForImageGeneration(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesGenerations,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://api.imagerouter.io",
		},
	}

	got, err := adaptor.GetRequestURL(info)

	require.NoError(t, err)
	require.Equal(t, "https://api.imagerouter.io/v1/openai/images/generations", got)
}

func TestConvertImageRequestMapsModelAndForcesEphemeralBase64(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	n := uint(3)
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: ModelQwenImage2512,
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	request := dto.ImageRequest{
		Model:        ModelQwenImage2512,
		Prompt:       "a quiet mountain at sunset",
		N:            &n,
		Quality:      "auto",
		Size:         "auto",
		OutputFormat: mustRawJSON(t, "webp"),
	}

	got, err := adaptor.ConvertImageRequest(gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New()), info, request)
	require.NoError(t, err)

	body, err := common.Marshal(got)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, common.Unmarshal(body, &payload))

	require.Equal(t, UpstreamModelQwenImage2512, payload["model"])
	require.Equal(t, "b64_ephemeral", payload["response_format"])
	require.Equal(t, "auto", payload["quality"])
	require.Equal(t, "auto", payload["size"])
	require.Equal(t, float64(1), payload["n"])
	require.Equal(t, "webp", payload["output_format"])
	require.Equal(t, UpstreamModelQwenImage2512, info.UpstreamModelName)
}

func TestDoResponseForImageGenerationUsesUpstreamBase64AndKeepsConfiguredPrice(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesGenerations,
		StartTime: time.Unix(1700000000, 0),
	}
	info.PriceData.UsePrice = true
	info.PriceData.ModelPrice = 0.0064
	info.PriceData.GroupRatioInfo.GroupRatio = 1
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"created": 1778579086,
			"data": [{"b64_json": "aW1hZ2U=", "revised_prompt": null}],
			"cost": 0.0103,
			"latency": 17058
		}`)),
	}

	adaptor := &Adaptor{}
	usage, apiErr := adaptor.DoResponse(c, resp, info)

	require.Nil(t, apiErr)
	require.Equal(t, 1, usage.(*dto.Usage).TotalTokens)
	require.Equal(t, 1, usage.(*dto.Usage).PromptTokens)
	require.Nil(t, usage.(*dto.Usage).Cost)
	require.True(t, info.PriceData.UsePrice)
	require.Equal(t, 0.0064, info.PriceData.ModelPrice)
	require.Equal(t, 1.0, info.PriceData.OtherRatios["n"])

	var payload map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.NotContains(t, payload, "cost")
	require.NotContains(t, payload, "latency")
	usagePayload := payload["usage"].(map[string]any)
	require.Equal(t, float64(1), usagePayload["prompt_tokens"])
	require.Equal(t, float64(1), usagePayload["total_tokens"])
	require.NotContains(t, usagePayload, "cost")

	item := payload["data"].(map[string]any)
	require.Equal(t, "aW1hZ2U=", item["b64_json"])
	require.NotContains(t, item, "url")
	require.NotContains(t, item, "revised_prompt")
}

func TestDoResponseForImageGenerationDownloadsURLFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()

	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	fetchSetting.EnableSSRFProtection = false
	defer func() {
		*fetchSetting = originalFetchSetting
	}()
	originalMaxFileDownloadMB := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 64
	defer func() {
		constant.MaxFileDownloadMB = originalMaxFileDownloadMB
	}()

	imageBytes := []byte("fake-webp-image")
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/webp")
		_, _ = w.Write(imageBytes)
	}))
	defer imageServer.Close()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesGenerations,
		StartTime: time.Unix(1700000000, 0),
	}
	info.PriceData.UsePrice = true
	info.PriceData.ModelPrice = 0.0064
	info.PriceData.GroupRatioInfo.GroupRatio = 1
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"created": 1778579086,
			"data": [{"url": "` + imageServer.URL + `/image.webp"}],
			"cost": "0.0052"
		}`)),
	}

	adaptor := &Adaptor{}
	_, apiErr := adaptor.DoResponse(c, resp, info)

	require.Nil(t, apiErr)
	require.Equal(t, 0.0064, info.PriceData.ModelPrice)

	var payload map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	item := payload["data"].(map[string]any)
	require.Equal(t, base64.StdEncoding.EncodeToString(imageBytes), item["b64_json"])
	require.NotContains(t, item, "url")
}

func mustRawJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := common.Marshal(value)
	require.NoError(t, err)
	return raw
}
