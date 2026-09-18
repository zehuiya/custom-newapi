# Qwen3 TTS E2E

Verification reports: [initial adapter](REPORT.md), [PCM header fix](PCM_FIX_REPORT.md).

This suite starts the complete NewAPI application with PostgreSQL, Redis and a
Python DashScope mock. It uses the admin API to create channels and pricing,
calls `/v1/audio/speech`, and checks both returned audio and consumption logs.
No production servers or credentials are used.

## Run

From the repository root, compile the current backend for Linux. The existing
`web/dist` is required by the application embed directive; build the frontend
with Bun first if it is absent.

PowerShell:

```powershell
New-Item -ItemType Directory -Path .test/qwen-tts-e2e -Force
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
go build -ldflags '-s -w' -o .test/qwen-tts-e2e/new-api .
docker compose -p qwen-tts-e2e -f docker-compose.qwen-tts-e2e.yml up -d --build --wait
py -3 -X utf8 -u tests/qwen_tts_e2e/run_e2e.py
```

Linux/macOS:

```sh
mkdir -p .test/qwen-tts-e2e
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags '-s -w' -o .test/qwen-tts-e2e/new-api .
docker compose -p qwen-tts-e2e -f docker-compose.qwen-tts-e2e.yml up -d --build --wait
python3 tests/qwen_tts_e2e/run_e2e.py
```

The test image uses the existing `20260914` image for its runtime dependencies
and replaces `/new-api` with the binary built from the working tree. It is not
a deployment of the old backend. The build context contains only the local
test artifacts; only the executable is copied into the image.

- NewAPI: `http://127.0.0.1:13009`
- Mock: `http://127.0.0.1:18089`
- Local test administrator: `root` / `QwenTts123!`
- JSON results, request/response files, consumption logs:
  `.test/qwen-tts-e2e/results/`

This suite uses persistent, isolated Compose volumes. Pricing is reset on each
run, and existing test channels/tokens are reused. The mock-only private-IP
download allowance is explicitly tested against the production SSRF defaults.

## Channel Setup

Select the Ali channel (type 17), configure the corresponding DashScope base
URL and API key, and add `qwen3-tts-flash` to its model list. Configure its input
price before use; no unverified default price is added by this change.

Example request:

```json
{
  "model": "qwen3-tts-flash",
  "input": "Hello, this is a speech synthesis test.",
  "voice": "Cherry",
  "language_type": "English",
  "response_format": "wav"
}
```

Non-stream mode defaults to WAV, which is downloaded from the upstream audio
URL and returned as binary `audio/wav`. Non-stream `response_format: "pcm"`
requests upstream SSE and concatenates decoded audio into binary `audio/pcm`.
SSE audio can be WAV-wrapped: the adapter removes its RIFF/WAV header before
returning PCM in either mode. Raw PCM passes through unchanged. WAV headers can
span multiple chunks and include metadata, and streaming placeholder sizes are
supported. No transcoding or change to usage/billing is performed.

For downstream SSE, add `"stream_format": "sse"` and use
`"response_format": "pcm"` (or omit the format). The response has `audio.delta`
events with Base64 audio followed by `audio.done` and character usage. PCM is
24 kHz, 16-bit little-endian, mono. Plain `stream: true` is not the TTS streaming
selector; use `stream_format` as with the existing audio request DTO.

This adapter supports `qwen3-tts-flash` and its dated snapshots. It does not
silently transcode to MP3, change speed, or apply instructions: unsupported
parameters return HTTP 400. Use a provider voice such as `Cherry`, rather than
an OpenAI-specific voice such as `alloy`.

The upstream `usage.characters` is the input billing quantity. The internal
`prompt_tokens` slot stores that quantity, matching existing character-based
TTS billing; it does not mean tokenizer tokens for this model. Completion
tokens remain zero. Missing character usage falls back to Unicode character
count; explicit zero is preserved, and negative usage is rejected. Pricing
ratio, group multiplier, per-call and expression pricing all reuse the existing
billing path. For ratio pricing, `ModelRatio = price_in_USD_per_1M_characters / 2`.

The mock defaults to WAV-wrapped SSE with the real channel's placeholder sizes.
The suite additionally covers raw PCM, finite sizes, fragmented/extended WAV
headers, metadata, malformed headers and concurrent mixing of both formats.
An optional `--recorded-sse PATH` replays saved real-channel audio through the
mock. Mount that file read-only into the mock and set its
`QWEN_TTS_RECORDED_SSE` environment variable to the container path first.

References:

- [Qwen TTS request/response API](https://help.aliyun.com/zh/model-studio/qwen-tts-api)
- [Qwen TTS streaming playback and format](https://help.aliyun.com/zh/model-studio/non-realtime-tts-user-guide)
