package doubao

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/samber/lo"
)

// ============================
// Request / Response structures
// ============================

type ContentItem struct {
	Type     string    `json:"type,omitempty"`
	Text     string    `json:"text,omitempty"`
	ImageURL *MediaURL `json:"image_url,omitempty"`
	VideoURL *MediaURL `json:"video_url,omitempty"`
	AudioURL *MediaURL `json:"audio_url,omitempty"`
	Role     string    `json:"role,omitempty"`
}

type MediaURL struct {
	URL string `json:"url,omitempty"`
}

type requestPayload struct {
	Model                 string         `json:"model"`
	Content               []ContentItem  `json:"content,omitempty"`
	CallbackURL           string         `json:"callback_url,omitempty"`
	ReturnLastFrame       *dto.BoolValue `json:"return_last_frame,omitempty"`
	ServiceTier           string         `json:"service_tier,omitempty"`
	ExecutionExpiresAfter *dto.IntValue  `json:"execution_expires_after,omitempty"`
	GenerateAudio         *dto.BoolValue `json:"generate_audio,omitempty"`
	Draft                 *dto.BoolValue `json:"draft,omitempty"`
	Tools                 []struct {
		Type string `json:"type,omitempty"`
	} `json:"tools,omitempty"`
	Resolution  string         `json:"resolution,omitempty"`
	Ratio       string         `json:"ratio,omitempty"`
	Duration    *dto.IntValue  `json:"duration,omitempty"`
	Frames      *dto.IntValue  `json:"frames,omitempty"`
	Seed        *dto.IntValue  `json:"seed,omitempty"`
	CameraFixed *dto.BoolValue `json:"camera_fixed,omitempty"`
	Watermark   *dto.BoolValue `json:"watermark,omitempty"`
}

type responsePayload struct {
	ID string `json:"id"` // task_id
}

type responseTask struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Status  string `json:"status"`
	Content struct {
		VideoURL string `json:"video_url"`
	} `json:"content"`
	Seed            int    `json:"seed"`
	Resolution      string `json:"resolution"`
	Duration        int    `json:"duration"`
	Ratio           string `json:"ratio"`
	FramesPerSecond int    `json:"framespersecond"`
	ServiceTier     string `json:"service_tier"`
	Tools           []struct {
		Type string `json:"type"`
	} `json:"tools"`
	Usage struct {
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		ToolUsage        struct {
			WebSearch int `json:"web_search"`
		} `json:"tool_usage"`
	} `json:"usage"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

// ValidateRequestAndSetAction parses body, validates fields and sets default action.
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	// Doubao 视频生成 API 使用 content 数组格式，需要自定义验证逻辑
	// 不能使用通用的 ValidateBasicTaskRequest（它要求 prompt 字段）
	
	var payload requestPayload
	if err := common.UnmarshalBodyReusable(c, &payload); err != nil {
		return &dto.TaskError{
			Code:       "invalid_request",
			Message:    "failed to parse request body",
			StatusCode: http.StatusBadRequest,
			LocalError: true,
		}
	}

	// 验证必填字段
	if payload.Model == "" {
		return &dto.TaskError{
			Code:       "invalid_request",
			Message:    "model is required",
			StatusCode: http.StatusBadRequest,
			LocalError: true,
		}
	}

	if len(payload.Content) == 0 {
		return &dto.TaskError{
			Code:       "invalid_request",
			Message:    "content is required",
			StatusCode: http.StatusBadRequest,
			LocalError: true,
		}
	}

	// 验证 content 中至少有一个文本类型（提示词）
	hasText := false
	for _, item := range payload.Content {
		if item.Type == "text" && strings.TrimSpace(item.Text) != "" {
			hasText = true
			break
		}
	}
	if !hasText {
		return &dto.TaskError{
			Code:       "invalid_request",
			Message:    "content must contain at least one text item with non-empty text",
			StatusCode: http.StatusBadRequest,
			LocalError: true,
		}
	}

	// 构造兼容的 TaskSubmitReq 对象（用于后续计费逻辑）
	req := relaycommon.TaskSubmitReq{
		Model:    payload.Model,
		Metadata: buildMetadataFromPayload(&payload),
	}

	// 提取第一个文本作为 Prompt（用于兼容通用逻辑）
	for _, item := range payload.Content {
		if item.Type == "text" {
			req.Prompt = item.Text
			break
		}
	}

	// 存储到 context
	c.Set("task_request", req)
	info.Action = constant.TaskActionGenerate
	return nil
}

// buildMetadataFromPayload 将 payload 转换为 metadata 格式，供计费逻辑使用
func buildMetadataFromPayload(payload *requestPayload) map[string]interface{} {
	metadata := make(map[string]interface{})

	// 将整个 content 数组存入 metadata
	metadata["content"] = payload.Content

	// 其他参数
	if payload.Resolution != "" {
		metadata["resolution"] = payload.Resolution
	}
	if payload.Ratio != "" {
		metadata["ratio"] = payload.Ratio
	}
	if payload.Duration != nil {
		metadata["duration"] = int(*payload.Duration)
	}
	if payload.GenerateAudio != nil {
		metadata["generate_audio"] = bool(*payload.GenerateAudio)
	}
	if payload.Seed != nil {
		metadata["seed"] = int(*payload.Seed)
	}
	if payload.CameraFixed != nil {
		metadata["camera_fixed"] = bool(*payload.CameraFixed)
	}
	if payload.Watermark != nil {
		metadata["watermark"] = bool(*payload.Watermark)
	}
	if payload.ServiceTier != "" {
		metadata["service_tier"] = payload.ServiceTier
	}

	return metadata
}

// BuildRequestURL constructs the upstream URL.
func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s/api/v3/contents/generations/tasks", a.baseURL), nil
}

// BuildRequestHeader sets required headers.
func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

// EstimateBilling 根据模型和请求参数（分辨率、视频输入、音频）计算 OtherRatios。
// 管理员应为每个模型配置"基准场景"的倍率（详见 constants.go 中的 BaseScenario），
// 系统会根据实际请求自动调整倍率。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}

	// 获取模型的计费配置
	config, ok := GetSeedanceBillingConfig(info.OriginModelName)
	if !ok {
		// 模型不在配置中，使用默认计费（无 OtherRatios）
		return nil
	}

	otherRatios := make(map[string]float64)

	// 1. 分辨率差异化计费
	if config.SupportResolution {
		resolution := parseResolution(req)
		if resRatio, exists := config.ResolutionRatios[resolution]; exists && resRatio != 1.0 {
			otherRatios["resolution"] = resRatio
		}
	}

	// 2. 视频输入差异化计费
	if config.SupportVideoInput {
		hasVideo := hasVideoInMetadata(req.Metadata)
		if hasVideo && config.VideoInputRatio != 0 && config.VideoInputRatio != 1.0 {
			otherRatios["video_input"] = config.VideoInputRatio
		}
	}

	// 3. 音频差异化计费（仅 1.5 pro）
	if config.SupportAudio {
		hasAudio := parseGenerateAudio(req)
		// 基准是"有声"，无声时打折
		if !hasAudio && config.NoAudioRatio != 0 && config.NoAudioRatio != 1.0 {
			otherRatios["audio"] = config.NoAudioRatio
		}
	}

	// 如果没有任何差异化，返回 nil
	if len(otherRatios) == 0 {
		return nil
	}

	return otherRatios
}

// parseResolution 从请求中提取分辨率参数
// 优先级：metadata.resolution > req.Size
// 返回规范化的值：480p / 720p / 1080p
func parseResolution(req relaycommon.TaskSubmitReq) string {
	// 1. 从 metadata.resolution 读取
	if req.Metadata != nil {
		if res, ok := req.Metadata["resolution"].(string); ok && res != "" {
			return normalizeResolution(res)
		}
	}

	// 2. 从 req.Size 读取（可能是 "1920x1080" 格式）
	if req.Size != "" {
		return sizeToResolution(req.Size)
	}

	// 3. 默认 720p
	return "720p"
}

// normalizeResolution 规范化分辨率字符串（统一为小写 + p）
func normalizeResolution(res string) string {
	// 转小写
	res = strings.ToLower(res)
	// 确保有 "p" 后缀
	if !strings.Contains(res, "p") {
		res = res + "p"
	}
	return res
}

// sizeToResolution 将 "WxH" 格式转换为分辨率标签
func sizeToResolution(size string) string {
	// 提取高度数字，判断分辨率等级
	// 例如：1920x1080 → 1080p，1280x720 → 720p
	parts := strings.Split(strings.ToLower(size), "x")
	if len(parts) != 2 {
		return "720p" // 默认
	}

	heightStr := strings.TrimSpace(parts[1])
	var height int
	if _, err := fmt.Sscanf(heightStr, "%d", &height); err != nil {
		return "720p"
	}

	// 根据高度判断分辨率等级
	if height >= 1080 {
		return "1080p"
	}
	if height >= 720 {
		return "720p"
	}
	return "480p"
}

// parseGenerateAudio 从请求中提取 generate_audio 参数
func parseGenerateAudio(req relaycommon.TaskSubmitReq) bool {
	if req.Metadata == nil {
		return false
	}
	if audio, ok := req.Metadata["generate_audio"].(bool); ok {
		return audio
	}
	return false
}

// hasVideoInMetadata 直接检查 metadata 的 content 数组是否包含 video_url 条目，
// 避免构建完整的上游 requestPayload。
func hasVideoInMetadata(metadata map[string]interface{}) bool {
	if metadata == nil {
		return false
	}
	contentRaw, ok := metadata["content"]
	if !ok {
		return false
	}
	contentSlice, ok := contentRaw.([]interface{})
	if !ok {
		return false
	}
	for _, item := range contentSlice {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if itemMap["type"] == "video_url" {
			return true
		}
		if _, has := itemMap["video_url"]; has {
			return true
		}
	}
	return false
}

// BuildRequestBody converts request into Doubao specific format.
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}

	body, err := a.convertToRequestPayload(&req)
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}
	if info.IsModelMapped {
		body.Model = info.UpstreamModelName
	} else {
		info.UpstreamModelName = body.Model
	}
	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

// DoRequest delegates to common helper.
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse handles upstream response, returns taskID etc.
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	// Parse Doubao response
	var dResp responsePayload
	if err := common.Unmarshal(responseBody, &dResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	if dResp.ID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName

	c.JSON(http.StatusOK, ov)
	return dResp.ID, responseBody, nil
}

// FetchTask fetch task status
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s/api/v3/contents/generations/tasks/%s", baseUrl, taskID)

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq) (*requestPayload, error) {
	r := requestPayload{
		Model:   req.Model,
		Content: []ContentItem{},
	}

	// Add images if present
	if req.HasImage() {
		for _, imgURL := range req.Images {
			r.Content = append(r.Content, ContentItem{
				Type: "image_url",
				ImageURL: &MediaURL{
					URL: imgURL,
				},
			})
		}
	}

	metadata := req.Metadata
	if err := taskcommon.UnmarshalMetadata(metadata, &r); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}

	if sec, _ := strconv.Atoi(req.Seconds); sec > 0 {
		r.Duration = lo.ToPtr(dto.IntValue(sec))
	}

	r.Content = lo.Reject(r.Content, func(c ContentItem, _ int) bool { return c.Type == "text" })
	r.Content = append(r.Content, ContentItem{
		Type: "text",
		Text: req.Prompt,
	})

	return &r, nil
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	resTask := responseTask{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}

	// Map Doubao status to internal status
	switch resTask.Status {
	case "pending", "queued":
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "10%"
	case "processing", "running":
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "50%"
	case "succeeded":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = resTask.Content.VideoURL
		// 解析 usage 信息用于按倍率计费
		taskResult.CompletionTokens = resTask.Usage.CompletionTokens
		taskResult.TotalTokens = resTask.Usage.TotalTokens
	case "failed":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = resTask.Error.Message
	default:
		// Unknown status, treat as processing
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}

	return &taskResult, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var dResp responseTask
	if err := common.Unmarshal(originTask.Data, &dResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal doubao task data failed")
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.TaskID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	openAIVideo.SetMetadata("url", dResp.Content.VideoURL)
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt
	openAIVideo.Model = originTask.Properties.OriginModelName

	if dResp.Status == "failed" {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: dResp.Error.Message,
			Code:    dResp.Error.Code,
		}
	}

	return common.Marshal(openAIVideo)
}
