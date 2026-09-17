package ali

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

type qwenTTSRequest struct {
	Model string       `json:"model"`
	Input qwenTTSInput `json:"input"`
}

type qwenTTSInput struct {
	Text         string  `json:"text"`
	Voice        string  `json:"voice"`
	LanguageType *string `json:"language_type,omitempty"`
}

type qwenTTSResponse struct {
	StatusCode int    `json:"status_code"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Output     struct {
		FinishReason string `json:"finish_reason"`
		Audio        struct {
			Data string `json:"data"`
			URL  string `json:"url"`
		} `json:"audio"`
	} `json:"output"`
	Usage struct {
		Characters *int `json:"characters"`
	} `json:"usage"`
}

func (a *Adaptor) convertTTSRequest(info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	invalid := func(message string) (io.Reader, error) {
		return nil, types.NewErrorWithStatusCode(errors.New(message), types.ErrorCodeInvalidRequest, http.StatusBadRequest)
	}
	if info.RelayMode != relayconstant.RelayModeAudioSpeech ||
		(request.Model != "qwen3-tts-flash" && !strings.HasPrefix(request.Model, "qwen3-tts-flash-20")) {
		return invalid("Ali speech synthesis supports qwen3-tts-flash and its dated snapshots only")
	}
	if strings.TrimSpace(request.Input) == "" || strings.TrimSpace(request.Voice) == "" {
		return invalid("input and voice are required for Qwen TTS")
	}
	if request.Speed != nil && *request.Speed != 1 {
		return invalid("qwen3-tts-flash does not support speed adjustment")
	}
	if request.Instructions != "" {
		return invalid("qwen3-tts-flash does not support instructions")
	}
	if request.StreamFormat != "" && request.StreamFormat != "sse" {
		return invalid("Qwen TTS stream_format must be sse or omitted")
	}
	if info.IsStream {
		if request.ResponseFormat != "" && request.ResponseFormat != "pcm" {
			return invalid("Qwen TTS SSE supports response_format pcm only")
		}
	} else if request.ResponseFormat != "" && request.ResponseFormat != "wav" && request.ResponseFormat != "pcm" {
		return invalid("Qwen TTS supports response_format wav or pcm only")
	}
	// DashScope returns PCM in SSE chunks and a WAV URL in non-stream mode.
	a.TTSSSE = info.IsStream || request.ResponseFormat == "pcm"
	payload, err := common.Marshal(qwenTTSRequest{
		Model: request.Model,
		Input: qwenTTSInput{Text: request.Input, Voice: request.Voice, LanguageType: request.LanguageType},
	})
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(payload), nil
}

func (r *qwenTTSResponse) validate() error {
	if r.Code != "" || (r.StatusCode != 0 && r.StatusCode != http.StatusOK) {
		return fmt.Errorf("Qwen TTS upstream error: %s %s (status %d)", r.Code, r.Message, r.StatusCode)
	}
	if r.Usage.Characters != nil && *r.Usage.Characters < 0 {
		return errors.New("Qwen TTS returned negative characters usage")
	}
	return nil
}

func ttsUsage(c *gin.Context, request *dto.AudioRequest, characters *int) *dto.Usage {
	count := utf8.RuneCountInString(request.Input)
	if characters != nil {
		count = *characters
	} else {
		common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
	}
	c.Set("tts_usage_characters", count)
	// Reuse character-based TTS pricing: one prompt unit is one input character.
	return &dto.Usage{PromptTokens: count, TotalTokens: count, InputTokens: count}
}

func ttsResponseError(c *gin.Context, err error) *types.NewAPIError {
	result := types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	if c.Writer.Written() {
		types.ErrOptionWithSkipRetry()(result)
	}
	return result
}

func ttsMaxBytes() int64 {
	maxMB := constant.MaxFileDownloadMB
	if maxMB <= 0 {
		maxMB = 64
	}
	return int64(maxMB) << 20
}

func readTTSAudio(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, ttsMaxBytes()+1))
	if err == nil && int64(len(data)) > ttsMaxBytes() {
		err = errors.New("Qwen TTS response exceeds the file download size limit")
	}
	return data, err
}

func downloadTTS(c *gin.Context, info *relaycommon.RelayInfo, audioURL string) ([]byte, error) {
	fetch := system_setting.GetFetchSetting()
	if err := common.ValidateURLWithFetchSetting(audioURL, fetch.EnableSSRFProtection, fetch.AllowPrivateIp,
		fetch.DomainFilterMode, fetch.IpFilterMode, fetch.DomainList, fetch.IpList, fetch.AllowedPorts, fetch.ApplyIPFilterForDomain); err != nil {
		return nil, fmt.Errorf("Qwen TTS audio URL rejected: %w", err)
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, audioURL, nil)
	if err != nil {
		return nil, err
	}
	client, err := service.GetHttpClientWithProxy(info.ChannelSetting.Proxy)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Qwen TTS audio download returned status %d", response.StatusCode)
	}
	data, err := readTTSAudio(response.Body)
	if err != nil {
		return nil, err
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errors.New("Qwen TTS audio download is not a WAV file")
	}
	return data, nil
}

func handleTTSResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	request, ok := info.Request.(*dto.AudioRequest)
	if !ok {
		return nil, ttsResponseError(c, errors.New("invalid Qwen TTS request"))
	}
	if channel.IsEventStreamResponse(resp) {
		return handleTTSStream(c, resp, info, request)
	}
	defer resp.Body.Close()
	if info.IsStream || request.ResponseFormat == "pcm" {
		return nil, ttsResponseError(c, errors.New("Qwen TTS upstream did not return the requested SSE audio"))
	}
	var response qwenTTSResponse
	if err := common.DecodeJson(io.LimitReader(resp.Body, ttsMaxBytes()), &response); err != nil {
		return nil, ttsResponseError(c, err)
	}
	if err := response.validate(); err != nil {
		return nil, ttsResponseError(c, err)
	}
	if response.Output.Audio.URL == "" {
		return nil, ttsResponseError(c, errors.New("Qwen TTS response contains no audio URL"))
	}
	audio, err := downloadTTS(c, info, response.Output.Audio.URL)
	if err != nil {
		return nil, ttsResponseError(c, err)
	}
	c.Data(http.StatusOK, "audio/wav", audio)
	return ttsUsage(c, request, response.Usage.Characters), nil
}

func handleTTSStream(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, request *dto.AudioRequest) (any, *types.NewAPIError) {
	var audio bytes.Buffer
	var characters *int
	var audioBytes int64
	finished := false
	err := channel.ConsumeBufferedSSE(c, resp, info, func(data string) (bool, error) {
		var response qwenTTSResponse
		if err := common.UnmarshalJsonStr(data, &response); err != nil {
			return false, err
		}
		if err := response.validate(); err != nil {
			return false, err
		}
		if response.Usage.Characters != nil {
			characters = response.Usage.Characters
		}
		if encoded := response.Output.Audio.Data; encoded != "" {
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return false, fmt.Errorf("invalid Qwen TTS audio base64: %w", err)
			}
			audioBytes += int64(len(decoded))
			if audioBytes > ttsMaxBytes() {
				return false, errors.New("Qwen TTS audio exceeds the file download size limit")
			}
			if info.IsStream {
				helper.SetEventStreamHeaders(c)
				if err := helper.ObjectData(c, map[string]any{"type": "audio.delta", "audio": encoded}); err != nil {
					return false, err
				}
			} else {
				_, _ = audio.Write(decoded)
			}
		}
		finished = response.Output.FinishReason == "stop"
		return finished, nil
	})
	if err == nil && (!finished || audioBytes == 0) {
		err = errors.New("Qwen TTS stream ended without complete audio")
	}
	if err != nil {
		if info.IsStream && c.Writer.Written() {
			_ = helper.ObjectData(c, map[string]any{"type": "error", "error": map[string]string{"message": err.Error()}})
		}
		return nil, ttsResponseError(c, err)
	}
	usage := ttsUsage(c, request, characters)
	if info.IsStream {
		err = helper.ObjectData(c, map[string]any{"type": "audio.done", "usage": map[string]int{
			"input_tokens": usage.PromptTokens, "output_tokens": 0,
			"total_tokens": usage.TotalTokens, "characters": usage.PromptTokens,
		}})
		if err != nil {
			return nil, ttsResponseError(c, err)
		}
	} else {
		c.Data(http.StatusOK, "audio/pcm", audio.Bytes())
	}
	return usage, nil
}
