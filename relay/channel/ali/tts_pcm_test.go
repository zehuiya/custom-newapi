package ali

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
)

func ttsWAVFixture(pcm []byte) []byte {
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+len(pcm)))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 1)
	binary.LittleEndian.PutUint32(header[24:28], 24000)
	binary.LittleEndian.PutUint32(header[28:32], 48000)
	binary.LittleEndian.PutUint16(header[32:34], 2)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(len(pcm)))
	return append(header, pcm...)
}

func TestQwenTTSPCMNormalization(t *testing.T) {
	pcm := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	wave := ttsWAVFixture(pcm)
	placeholder := bytes.Clone(wave)
	binary.LittleEndian.PutUint32(placeholder[4:8], 0x7fffffbf)
	binary.LittleEndian.PutUint32(placeholder[40:44], 0x7fffff99)
	metadata := append(bytes.Clone(wave[:12]), []byte("JUNK\x03\x00\x00\x00abc\x00")...)
	metadata = append(metadata, wave[12:]...)
	extended := append(bytes.Clone(wave[:36]), 0, 0)
	extended = append(extended, wave[36:]...)
	binary.LittleEndian.PutUint32(extended[16:20], 18)
	for name, input := range map[string][]byte{
		"raw": pcm, "wave": wave, "placeholder": placeholder,
		"odd_metadata": metadata, "extended_fmt": extended,
		"short_raw": pcm[:2],
	} {
		for _, chunkSize := range []int{1, 3, 4, 7, 16, 44, 1024} {
			t.Run(name+"/"+strconv.Itoa(chunkSize), func(t *testing.T) {
				var stream ttsPCMStream
				var output []byte
				for start := 0; start < len(input); start += chunkSize {
					part, err := stream.push(input[start:min(start+chunkSize, len(input))])
					if err != nil {
						t.Fatal(err)
					}
					output = append(output, part...)
					if len(stream.header) > ttsMaxWAVHeaderBytes {
						t.Fatal("header buffering is not bounded")
					}
				}
				tail, err := stream.finish()
				if err != nil {
					t.Fatal(err)
				}
				output = append(output, tail...)
				expected := pcm
				if name == "short_raw" {
					expected = pcm[:2]
				}
				if !bytes.Equal(output, expected) || len(stream.header) != 0 {
					t.Fatalf("PCM changed or header retained: %x", output)
				}
			})
		}
	}
}

func TestQwenTTSPCMInvalidWAV(t *testing.T) {
	wave := ttsWAVFixture([]byte{1, 2})
	for name, mutate := range map[string]func([]byte) []byte{
		"wrong_riff_type": func(b []byte) []byte { copy(b[8:12], "AVI "); return b },
		"wrong_encoding":  func(b []byte) []byte { b[20] = 3; return b },
		"wrong_channels":  func(b []byte) []byte { b[22] = 2; return b },
		"wrong_rate":      func(b []byte) []byte { b[24] = 1; return b },
		"wrong_depth":     func(b []byte) []byte { b[34] = 8; return b },
		"short_fmt":       func(b []byte) []byte { b[16] = 2; return b },
		"oversize_fmt":    func(b []byte) []byte { binary.LittleEndian.PutUint32(b[16:20], 0xffffffff); return b },
		"missing_fmt":     func(b []byte) []byte { return append(b[:12], b[36:]...) },
		"incomplete":      func(b []byte) []byte { return b[:30] },
	} {
		t.Run(name, func(t *testing.T) {
			var stream ttsPCMStream
			pcm, err := stream.push(mutate(bytes.Clone(wave)))
			if err == nil {
				_, err = stream.finish()
			}
			if err == nil || len(pcm) != 0 {
				t.Fatalf("invalid WAV was emitted as PCM: %x %v", pcm, err)
			}
		})
	}
}

func TestQwenTTSWAVStreamResponse(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 3
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	pcm := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	wave := ttsWAVFixture(pcm)
	for _, stream := range []bool{false, true} {
		for _, size := range []int{1, 3, 44, 1024} {
			request := dto.AudioRequest{Model: "qwen3-tts-flash", Input: "hello", Voice: "Cherry", ResponseFormat: "pcm"}
			if stream {
				request.StreamFormat = "sse"
			}
			c, w, info := ttsTestContext(&request)
			var body strings.Builder
			for start := 0; start < len(wave); start += size {
				body.WriteString(ttsChunk(t, base64.StdEncoding.EncodeToString(wave[start:min(start+size, len(wave))]), "", nil))
			}
			body.WriteString(ttsChunk(t, "", "stop", common.GetPointer(37)))
			response := &http.Response{Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body.String()))}
			usage, err := handleTTSResponse(c, response, info)
			if err != nil || usage.(*dto.Usage).PromptTokens != 37 {
				t.Fatalf("incorrect response or changed usage: %v", err)
			}
			output := w.Body.Bytes()
			if stream {
				output = nil
				for _, line := range strings.Split(w.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var event struct {
						Type  string `json:"type"`
						Audio string `json:"audio"`
					}
					if err := common.UnmarshalJsonStr(strings.TrimPrefix(line, "data: "), &event); err != nil {
						t.Fatal(err)
					}
					if event.Type == "audio.delta" {
						part, err := base64.StdEncoding.DecodeString(event.Audio)
						if err != nil || len(part) == 0 {
							t.Fatal("empty or invalid audio delta")
						}
						output = append(output, part...)
					}
				}
				if !strings.Contains(w.Body.String(), "audio.done") {
					t.Fatal("no successful completion")
				}
			}
			if !bytes.Equal(output, pcm) {
				t.Fatalf("not headerless PCM: %x", output)
			}
		}
	}
}
