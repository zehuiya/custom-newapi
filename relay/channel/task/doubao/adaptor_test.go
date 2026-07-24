package doubao

import (
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func TestBuildRequestURL(t *testing.T) {
	tests := []struct {
		name        string
		channelType int
		baseURL     string
		want        string
	}{
		{
			name:        "regular volcengine",
			channelType: constant.ChannelTypeVolcEngine,
			baseURL:     "https://ark.cn-beijing.volces.com",
			want:        "https://ark.cn-beijing.volces.com/api/v3/contents/generations/tasks",
		},
		{
			name:        "agent plan",
			channelType: constant.ChannelTypeVolcEngineAgentPlan,
			baseURL:     "https://ark.cn-beijing.volces.com",
			want:        "https://ark.cn-beijing.volces.com/api/plan/v3/contents/generations/tasks",
		},
		{
			name:        "agent plan accepts prefixed base",
			channelType: constant.ChannelTypeVolcEngineAgentPlan,
			baseURL:     "https://ark.cn-beijing.volces.com/api/plan/v3/",
			want:        "https://ark.cn-beijing.volces.com/api/plan/v3/contents/generations/tasks",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:    tt.channelType,
				ChannelBaseUrl: tt.baseURL,
			}}
			adaptor := &TaskAdaptor{}
			adaptor.Init(info)
			got, err := adaptor.BuildRequestURL(info)
			if err != nil {
				t.Fatalf("BuildRequestURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("BuildRequestURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFetchTaskUsesAgentPlanPathAndAuthorization(t *testing.T) {
	const taskID = "task-123"
	service.InitHttpClient()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/plan/v3/contents/generations/tasks/"+taskID {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization header = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"task-123","status":"succeeded"}`))
	}))
	defer server.Close()

	adaptor := &TaskAdaptor{}
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType: constant.ChannelTypeVolcEngineAgentPlan,
	}})
	resp, err := adaptor.FetchTask(server.URL, "test-key", map[string]any{"task_id": taskID}, "")
	if err != nil {
		t.Fatalf("FetchTask() error = %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestAgentPlanBillingAliases(t *testing.T) {
	tests := []struct {
		alias     string
		canonical string
	}{
		{alias: "doubao-seedance-1.5-pro", canonical: "doubao-seedance-1-5-pro-251215"},
		{alias: "doubao-seedance-2.0", canonical: "doubao-seedance-2-0-260128"},
		{alias: "doubao-seedance-2.0-fast", canonical: "doubao-seedance-2-0-fast-260128"},
	}

	for _, tt := range tests {
		t.Run(tt.alias, func(t *testing.T) {
			got, gotOK := GetSeedanceBillingConfig(tt.alias)
			want, wantOK := GetSeedanceBillingConfig(tt.canonical)
			if !gotOK || !wantOK {
				t.Fatalf("billing config missing: alias=%t canonical=%t", gotOK, wantOK)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("alias config = %#v, want %#v", got, want)
			}
		})
	}

	if _, ok := GetSeedanceBillingConfig("doubao-seedance-2.0-mini"); ok {
		t.Fatal("Agent Plan 2.0-mini must not inherit regular Ark differential billing")
	}
}

func TestSeedanceMiniBillingConfig(t *testing.T) {
	const modelName = "doubao-seedance-2-0-mini-260615"

	modelListed := false
	for _, model := range ModelList {
		if model == modelName {
			modelListed = true
			break
		}
	}
	if !modelListed {
		t.Fatalf("%s is missing from ModelList", modelName)
	}

	config, ok := GetSeedanceBillingConfig(modelName)
	if !ok {
		t.Fatalf("billing config missing for %s", modelName)
	}
	if config.BaseScenario != "不含视频输入" {
		t.Fatalf("BaseScenario = %q, want %q", config.BaseScenario, "不含视频输入")
	}
	if !config.SupportVideoInput {
		t.Fatal("SupportVideoInput = false, want true")
	}
	if config.SupportResolution {
		t.Fatal("SupportResolution = true, want false")
	}
	if math.Abs(config.VideoInputRatio-14.0/23.0) > 1e-12 {
		t.Fatalf("VideoInputRatio = %.15f, want %.15f", config.VideoInputRatio, 14.0/23.0)
	}
}

func TestEstimateBillingAgentPlanAudio(t *testing.T) {
	tests := []struct {
		name          string
		metadata      map[string]interface{}
		wantAudio     float64
		wantAudioRule bool
	}{
		{
			name:          "audio uses configured baseline",
			metadata:      map[string]interface{}{"generate_audio": true},
			wantAudioRule: false,
		},
		{
			name:          "silent video is half price",
			metadata:      map[string]interface{}{"generate_audio": false},
			wantAudio:     0.5,
			wantAudioRule: true,
		},
		{
			name:          "omitted audio flag follows upstream audio default",
			metadata:      map[string]interface{}{},
			wantAudioRule: false,
		},
		{
			name:          "nil metadata follows upstream audio default",
			metadata:      nil,
			wantAudioRule: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ratios := estimateBillingForTest(t, "doubao-seedance-1.5-pro", tt.metadata)
			got, exists := ratios["audio"]
			if exists != tt.wantAudioRule {
				t.Fatalf("audio ratio exists = %t, want %t; ratios=%v", exists, tt.wantAudioRule, ratios)
			}
			if exists && got != tt.wantAudio {
				t.Fatalf("audio ratio = %v, want %v", got, tt.wantAudio)
			}
		})
	}
}

func TestEstimateBillingVideoScenarios(t *testing.T) {
	videoContent := []ContentItem{
		{Type: "text", Text: "extend this video"},
		{Type: "video_url", VideoURL: &MediaURL{URL: "https://example.com/input.mp4"}},
	}
	tests := []struct {
		name      string
		model     string
		metadata  map[string]interface{}
		want      map[string]float64
		wantTotal float64
	}{
		{
			name:     "2.0 720p without video uses baseline",
			model:    "doubao-seedance-2.0",
			metadata: map[string]interface{}{"resolution": "720p"},
			want:     map[string]float64{},
		},
		{
			name:      "2.0 720p with video",
			model:     "doubao-seedance-2.0",
			metadata:  map[string]interface{}{"resolution": "720p", "content": videoContent},
			want:      map[string]float64{"video_input": 28.0 / 46.0},
			wantTotal: 28.0 / 46.0,
		},
		{
			name:      "2.0 1080p without video",
			model:     "doubao-seedance-2.0",
			metadata:  map[string]interface{}{"resolution": "1080p"},
			want:      map[string]float64{"resolution": 51.0 / 46.0},
			wantTotal: 51.0 / 46.0,
		},
		{
			name:     "2.0 1080p with video uses exact matrix price",
			model:    "doubao-seedance-2.0",
			metadata: map[string]interface{}{"resolution": "1080p", "content": videoContent},
			want: map[string]float64{
				"resolution":  51.0 / 46.0,
				"video_input": 31.0 / 51.0,
			},
			wantTotal: 31.0 / 46.0,
		},
		{
			name:      "2.0 fast with video",
			model:     "doubao-seedance-2.0-fast",
			metadata:  map[string]interface{}{"resolution": "720p", "content": videoContent},
			want:      map[string]float64{"video_input": 22.0 / 37.0},
			wantTotal: 22.0 / 37.0,
		},
		{
			name:  "2.0 mini without video uses baseline",
			model: "doubao-seedance-2-0-mini-260615",
			metadata: map[string]interface{}{"content": []ContentItem{
				{Type: "text", Text: "create a video"},
				{Type: "image_url", ImageURL: &MediaURL{URL: "https://example.com/reference.jpg"}},
				{Type: "audio_url", AudioURL: &MediaURL{URL: "https://example.com/reference.mp3"}},
			}},
			want: map[string]float64{},
		},
		{
			name:      "2.0 mini with video",
			model:     "doubao-seedance-2-0-mini-260615",
			metadata:  map[string]interface{}{"resolution": "1080p", "content": videoContent},
			want:      map[string]float64{"video_input": 14.0 / 23.0},
			wantTotal: 14.0 / 23.0,
		},
		{
			name:  "2.0 mini detects generic video metadata",
			model: "doubao-seedance-2-0-mini-260615",
			metadata: map[string]interface{}{"content": []interface{}{
				map[string]interface{}{
					"type":      "video_url",
					"video_url": map[string]interface{}{"url": "https://example.com/reference.mp4"},
				},
			}},
			want:      map[string]float64{"video_input": 14.0 / 23.0},
			wantTotal: 14.0 / 23.0,
		},
		{
			name:  "2.0 mini ignores empty video URL",
			model: "doubao-seedance-2-0-mini-260615",
			metadata: map[string]interface{}{"content": []ContentItem{
				{Type: "text", Text: "create a video"},
				{Type: "video_url", VideoURL: &MediaURL{URL: " "}},
			}},
			want: map[string]float64{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ratios := estimateBillingForTest(t, tt.model, tt.metadata)
			if len(ratios) != len(tt.want) {
				t.Fatalf("ratios = %v, want %v", ratios, tt.want)
			}
			total := 1.0
			for name, want := range tt.want {
				got, ok := ratios[name]
				if !ok || got != want {
					t.Fatalf("ratio %s = %v (exists=%t), want %v", name, got, ok, want)
				}
				total *= got
			}
			if len(tt.want) > 0 && math.Abs(total-tt.wantTotal) > 1e-12 {
				t.Fatalf("combined ratio = %.15f, want %.15f", total, tt.wantTotal)
			}
		})
	}
}

func TestEstimateBillingMiniFromValidatedRequest(t *testing.T) {
	const modelName = "doubao-seedance-2-0-mini-260615"
	requestBody := `{
		"model": "doubao-seedance-2-0-mini-260615",
		"content": [
			{"type": "text", "text": "extend the reference video"},
			{"type": "image_url", "image_url": {"url": "https://example.com/reference.jpg"}, "role": "reference_image"},
			{"type": "video_url", "video_url": {"url": "https://example.com/reference.mp4"}, "role": "reference_video"},
			{"type": "audio_url", "audio_url": {"url": "https://example.com/reference.mp3"}, "role": "reference_audio"}
		],
		"generate_audio": true,
		"watermark": false
	}`

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", strings.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		OriginModelName: modelName,
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}

	if taskErr := adaptor.ValidateRequestAndSetAction(c, info); taskErr != nil {
		t.Fatalf("ValidateRequestAndSetAction() error = %v", taskErr)
	}
	ratios := adaptor.EstimateBilling(c, info)
	got, ok := ratios["video_input"]
	if !ok {
		t.Fatalf("video_input ratio missing; ratios=%v", ratios)
	}
	if math.Abs(got-14.0/23.0) > 1e-12 {
		t.Fatalf("video_input ratio = %.15f, want %.15f", got, 14.0/23.0)
	}
	if len(ratios) != 1 {
		t.Fatalf("ratios = %v, want only video_input", ratios)
	}
}

func TestBuildRequestBodyPreservesGenerateAudioPresence(t *testing.T) {
	tests := []struct {
		name       string
		metadata   map[string]interface{}
		wantExists bool
		wantValue  bool
	}{
		{name: "omitted stays omitted"},
		{
			name:       "explicit false stays false",
			metadata:   map[string]interface{}{"generate_audio": false},
			wantExists: true,
			wantValue:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("task_request", relaycommon.TaskSubmitReq{
				Model:    "doubao-seedance-1.5-pro",
				Prompt:   "a test video",
				Metadata: tt.metadata,
			})
			info := &relaycommon.RelayInfo{
				OriginModelName: "doubao-seedance-1.5-pro",
				ChannelMeta:     &relaycommon.ChannelMeta{},
			}
			body, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
			if err != nil {
				t.Fatalf("BuildRequestBody() error = %v", err)
			}
			data, err := io.ReadAll(body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			var payload map[string]interface{}
			if err := common.Unmarshal(data, &payload); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			got, exists := payload["generate_audio"]
			if exists != tt.wantExists {
				t.Fatalf("generate_audio exists = %t, want %t; body=%s", exists, tt.wantExists, data)
			}
			if exists && got != tt.wantValue {
				t.Fatalf("generate_audio = %#v, want %t", got, tt.wantValue)
			}
		})
	}
}

func estimateBillingForTest(t *testing.T, model string, metadata map[string]interface{}) map[string]float64 {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", relaycommon.TaskSubmitReq{Model: model, Metadata: metadata})
	info := &relaycommon.RelayInfo{OriginModelName: model}
	return (&TaskAdaptor{}).EstimateBilling(c, info)
}
