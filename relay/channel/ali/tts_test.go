package ali

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func ttsTestContext(request *dto.AudioRequest) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
	return c, w, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{}, Request: request,
		RelayMode: relayconstant.RelayModeAudioSpeech,
		IsStream:  request.StreamFormat == "sse",
	}
}

func TestQwenTTSConversion(t *testing.T) {
	request := dto.AudioRequest{Model: "qwen3-tts-flash", Input: "hello", Voice: "Cherry", LanguageType: common.GetPointer("English")}
	c, _, info := ttsTestContext(&request)
	info.ChannelBaseUrl = "https://dashscope.aliyuncs.com"
	info.ApiKey = "mock-key"
	for _, stream := range []bool{false, true} {
		info.IsStream = stream
		a := &Adaptor{}
		reader, err := a.ConvertAudioRequest(c, info, request)
		if err != nil {
			t.Fatal(err)
		}
		var converted qwenTTSRequest
		if err := common.DecodeJson(reader, &converted); err != nil {
			t.Fatal(err)
		}
		if converted.Model != request.Model || converted.Input.Text != "hello" || converted.Input.Voice != "Cherry" || *converted.Input.LanguageType != "English" {
			t.Fatalf("incorrect conversion: %+v", converted)
		}
		url, err := a.GetRequestURL(info)
		if err != nil || url != info.ChannelBaseUrl+"/api/v1/services/aigc/multimodal-generation/generation" {
			t.Fatalf("incorrect URL: %s, %v", url, err)
		}
		header := make(http.Header)
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatal(err)
		}
		if header.Get("Authorization") != "Bearer mock-key" || (header.Get("X-DashScope-SSE") == "enable") != stream {
			t.Fatalf("incorrect headers: %v", header)
		}
	}
	info.IsStream = false
	request.ResponseFormat = "pcm"
	a := &Adaptor{}
	if _, err := a.ConvertAudioRequest(c, info, request); err != nil || !a.TTSSSE {
		t.Fatalf("non-stream PCM must request SSE upstream: %v", err)
	}
	request.Model = "qwen3-tts-flash-2025-11-27"
	if _, err := a.ConvertAudioRequest(c, info, request); err != nil {
		t.Fatal(err)
	}
}

func TestQwenTTSRejectUnsupportedRequests(t *testing.T) {
	cases := map[string]func(*dto.AudioRequest){
		"model":         func(r *dto.AudioRequest) { r.Model = "qwen-plus" },
		"realtime":      func(r *dto.AudioRequest) { r.Model = "qwen3-tts-flash-realtime" },
		"input":         func(r *dto.AudioRequest) { r.Input = " " },
		"voice":         func(r *dto.AudioRequest) { r.Voice = "" },
		"mp3":           func(r *dto.AudioRequest) { r.ResponseFormat = "mp3" },
		"speed-zero":    func(r *dto.AudioRequest) { r.Speed = common.GetPointer(0.0) },
		"instructions":  func(r *dto.AudioRequest) { r.Instructions = "fast" },
		"stream-format": func(r *dto.AudioRequest) { r.StreamFormat = "invalid" },
		"sse-wav":       func(r *dto.AudioRequest) { r.StreamFormat = "sse"; r.ResponseFormat = "wav" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := dto.AudioRequest{Model: "qwen3-tts-flash", Input: "hello", Voice: "Cherry"}
			mutate(&request)
			c, _, info := ttsTestContext(&request)
			_, err := (&Adaptor{}).ConvertAudioRequest(c, info, request)
			var apiErr *types.NewAPIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
				t.Fatalf("expected 400, got %v", err)
			}
		})
	}
}

func ttsChunk(t *testing.T, audio, finish string, characters *int) string {
	t.Helper()
	response := qwenTTSResponse{StatusCode: 200}
	response.Output.Audio.Data = audio
	response.Output.FinishReason = finish
	response.Usage.Characters = characters
	encoded, err := common.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return "data: " + string(encoded) + "\n\n"
}

func TestQwenTTSStreamAndCharacterUsage(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 3
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	encoded := base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4})
	for _, stream := range []bool{false, true} {
		for _, count := range []*int{nil, common.GetPointer(37), common.GetPointer(0)} {
			request := dto.AudioRequest{Model: "qwen3-tts-flash", Input: "hello世界", Voice: "Cherry", ResponseFormat: "pcm"}
			if stream {
				request.StreamFormat = "sse"
			}
			c, w, info := ttsTestContext(&request)
			body := ttsChunk(t, encoded, "", nil) + ttsChunk(t, encoded, "", nil) + ttsChunk(t, "", "stop", count)
			response := &http.Response{Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
			value, err := handleTTSResponse(c, response, info)
			if err != nil {
				t.Fatal(err)
			}
			usage := value.(*dto.Usage)
			expected := 7
			if count != nil {
				expected = *count
			}
			if usage.PromptTokens != expected || usage.TotalTokens != expected || usage.CompletionTokens != 0 || usage.CompletionTokenDetails.AudioTokens != 0 {
				t.Fatalf("incorrect character billing: %+v", usage)
			}
			if !info.StreamStatus.IsNormalEnd() {
				t.Fatalf("incorrect stream status: %+v", info.StreamStatus)
			}
			if stream {
				if strings.Count(w.Body.String(), "audio.delta") != 2 || !strings.Contains(w.Body.String(), "audio.done") {
					t.Fatalf("incorrect SSE: %s", w.Body.String())
				}
			} else if !bytes.Equal(w.Body.Bytes(), []byte{1, 2, 3, 4, 1, 2, 3, 4}) || w.Header().Get("Content-Type") != "audio/pcm" {
				t.Fatalf("incorrect PCM response: %v", w.Body.Bytes())
			}
		}
	}
}

func TestQwenTTSStreamFailures(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 3
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	for name, body := range map[string]string{
		"invalid-json":   "data: {invalid\n\n",
		"base64":         ttsChunk(t, "%%%", "stop", nil),
		"no-terminal":    ttsChunk(t, "AQIDBA==", "", nil),
		"empty-audio":    ttsChunk(t, "", "stop", nil),
		"negative-usage": ttsChunk(t, "AQIDBA==", "stop", common.GetPointer(-1)),
		"upstream-error": "data: {\"code\":\"InvalidParameter\",\"message\":\"bad voice\"}\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			request := dto.AudioRequest{Model: "qwen3-tts-flash", Input: "test", Voice: "Cherry", StreamFormat: "sse"}
			c, w, info := ttsTestContext(&request)
			response := &http.Response{Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
			_, err := handleTTSResponse(c, response, info)
			if err == nil || strings.Contains(w.Body.String(), "audio.done") {
				t.Fatalf("false successful completion: %v %s", err, w.Body.String())
			}
			if w.Body.Len() > 0 && !types.IsSkipRetryError(err) {
				t.Fatal("must not retry a partially written response")
			}
		})
	}
}

func TestQwenTTSDownloadAndSSRF(t *testing.T) {
	service.InitHttpClient()
	fetch := system_setting.GetFetchSetting()
	old := *fetch
	fetch.AllowPrivateIp = true
	fetch.AllowedPorts = []string{"1-65535"}
	t.Cleanup(func() { *fetch = old })
	wave := []byte("RIFF\x04\x00\x00\x00WAVEaudio")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("API key leaked to audio download")
		}
		if r.URL.Path == "/bad" {
			_, _ = w.Write([]byte("bad"))
			return
		}
		if r.URL.Path == "/missing" {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write(wave)
	}))
	defer server.Close()
	request := dto.AudioRequest{Model: "qwen3-tts-flash", Input: "test", Voice: "Cherry"}
	c, w, info := ttsTestContext(&request)
	response := qwenTTSResponse{StatusCode: 200}
	response.Output.Audio.URL = server.URL
	response.Usage.Characters = common.GetPointer(73)
	data, _ := common.Marshal(response)
	value, err := handleTTSResponse(c, &http.Response{Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, info)
	if err != nil || !bytes.Equal(w.Body.Bytes(), wave) || value.(*dto.Usage).PromptTokens != 73 {
		t.Fatalf("WAV response failed: %v", err)
	}
	for _, path := range []string{"/bad", "/missing"} {
		if _, err := downloadTTS(c, info, server.URL+path); err == nil {
			t.Fatal("invalid audio accepted")
		}
	}
	fetch.AllowPrivateIp = false
	if _, err := downloadTTS(c, info, "http://127.0.0.1/audio"); err == nil {
		t.Fatal("private audio URL must be blocked")
	}
	fetch.AllowPrivateIp = true
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	if _, err := downloadTTS(c, info, server.URL); err == nil {
		t.Fatal("canceled download accepted")
	}
}

func TestQwenTTSAudioSizeLimit(t *testing.T) {
	old := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 1
	t.Cleanup(func() { constant.MaxFileDownloadMB = old })
	if _, err := readTTSAudio(strings.NewReader(strings.Repeat("x", (1<<20)+1))); err == nil {
		t.Fatal("oversized audio accepted")
	}
}

func TestQwenTTSLanguageOmissionAndExplicitEmpty(t *testing.T) {
	for _, input := range []string{
		`{"model":"qwen3-tts-flash","input":"hello","voice":"Cherry"}`,
		`{"model":"qwen3-tts-flash","input":"hello","voice":"Cherry","language_type":""}`,
	} {
		var request dto.AudioRequest
		if err := common.UnmarshalJsonStr(input, &request); err != nil {
			t.Fatal(err)
		}
		c, _, info := ttsTestContext(&request)
		reader, err := (&Adaptor{}).ConvertAudioRequest(c, info, request)
		if err != nil {
			t.Fatal(err)
		}
		var converted map[string]any
		if err := common.DecodeJson(reader, &converted); err != nil {
			t.Fatal(err)
		}
		value, exists := converted["input"].(map[string]any)["language_type"]
		if exists != strings.Contains(input, "language_type") || (exists && value != "") {
			t.Fatalf("optional language changed: %v", converted)
		}
	}
}

func TestAliExistingURLsUnchanged(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"}}
	for mode, path := range map[int]string{
		relayconstant.RelayModeChatCompletions: "/compatible-mode/v1/chat/completions",
		relayconstant.RelayModeEmbeddings:      "/compatible-mode/v1/embeddings",
		relayconstant.RelayModeResponses:       "/api/v2/apps/protocols/compatible-mode/v1/responses",
	} {
		info.RelayMode = mode
		url, err := (&Adaptor{}).GetRequestURL(info)
		if err != nil || url != info.ChannelBaseUrl+path {
			t.Fatalf("existing URL changed: %s %v", url, err)
		}
	}
}
