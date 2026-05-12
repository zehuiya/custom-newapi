package runware

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
			ChannelBaseUrl: "https://api.runware.ai",
		},
	}

	got, err := adaptor.GetRequestURL(info)

	require.NoError(t, err)
	require.Equal(t, "https://api.runware.ai/v1", got)
}

func TestConvertImageRequestBuildsRunwareTask(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	n := uint(2)
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: ModelQwenImage2512,
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	request := dto.ImageRequest{
		Model:        ModelQwenImage2512,
		Prompt:       "a quiet mountain at sunset",
		N:            &n,
		Quality:      "high",
		Size:         "1024x768",
		OutputFormat: mustRawJSON(t, "webp"),
	}

	got, err := adaptor.ConvertImageRequest(gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New()), info, request)
	require.NoError(t, err)

	body, err := common.Marshal(got)
	require.NoError(t, err)

	var payload []map[string]any
	require.NoError(t, common.Unmarshal(body, &payload))
	require.Len(t, payload, 1)

	task := payload[0]
	require.Equal(t, "imageInference", task["taskType"])
	require.NotEmpty(t, task["taskUUID"])
	require.Equal(t, UpstreamModelQwenImage2512, task["model"])
	require.Equal(t, "a quiet mountain at sunset", task["positivePrompt"])
	require.Equal(t, float64(1024), task["width"])
	require.Equal(t, float64(768), task["height"])
	require.Equal(t, float64(2), task["numberResults"])
	require.Equal(t, "base64Data", task["outputType"])
	require.Equal(t, "WEBP", task["outputFormat"])
	require.Equal(t, "sync", task["deliveryMethod"])
	require.Equal(t, float64(99), task["outputQuality"])
	require.Equal(t, UpstreamModelQwenImage2512, info.UpstreamModelName)
}

func TestDoResponseForImageGenerationUsesBase64AndSanitizesResponse(t *testing.T) {
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
			"data": [{
				"taskType": "imageInference",
				"taskUUID": "39d7207a-87ef-4c93-8082-1431f9c1dc97",
				"imageUUID": "b7db282d-2943-4f12-992f-77df3ad3ec71",
				"imageBase64Data": "aW1hZ2U=",
				"cost": 0.0051
			}]
		}`)),
	}

	adaptor := &Adaptor{}
	usage, apiErr := adaptor.DoResponse(c, resp, info)

	require.Nil(t, apiErr)
	require.Equal(t, 1, usage.(*dto.Usage).TotalTokens)
	require.Equal(t, 1, usage.(*dto.Usage).PromptTokens)
	require.Nil(t, usage.(*dto.Usage).Cost)
	require.Equal(t, 1.0, info.PriceData.OtherRatios["n"])

	var payload map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.ElementsMatch(t, []string{"created", "data", "usage"}, mapKeys(payload))
	data := payload["data"].([]any)
	item := data[0].(map[string]any)
	require.Equal(t, "aW1hZ2U=", item["b64_json"])
	require.NotContains(t, item, "url")
	require.NotContains(t, payload, "cost")
	usagePayload := payload["usage"].(map[string]any)
	require.Equal(t, float64(1), usagePayload["prompt_tokens"])
	require.Equal(t, float64(1), usagePayload["total_tokens"])
	require.NotContains(t, usagePayload, "cost")
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
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"data": [{"imageURL": "` + imageServer.URL + `/image.webp"}]
		}`)),
	}

	adaptor := &Adaptor{}
	_, apiErr := adaptor.DoResponse(c, resp, info)

	require.Nil(t, apiErr)

	var payload map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	data := payload["data"].([]any)
	item := data[0].(map[string]any)
	require.Equal(t, base64.StdEncoding.EncodeToString(imageBytes), item["b64_json"])
	require.NotContains(t, item, "url")
}

func mustRawJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := common.Marshal(value)
	require.NoError(t, err)
	return raw
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
