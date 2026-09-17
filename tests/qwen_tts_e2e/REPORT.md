# Qwen3 TTS Verification Report

## Environment

- Executed on 2026-09-17, approximately 23:50 Beijing time.
- Branch: `onesapi`; base commit: `beea575760195f5d008d2667ccfa83bb875cf51b`.
- Complete local Docker Compose stack: NewAPI, PostgreSQL 15, Redis 7,
  and a threaded Python mock of the official DashScope TTS protocol.
- Application: `http://127.0.0.1:13009`; mock: `http://127.0.0.1:18089`.
- Channels, token and pricing were configured through the real admin API.
- The Linux/amd64 test binary was compiled from the modified working tree.
  Its SHA-256 matched `/new-api` inside the running container:
  `fff0d6bd8001a5a2bda171bc9cbde8296b26d65f9491b1e97ac44704e42d3503`.
- No production service was contacted, restarted or upgraded.

## Results

**42 cases passed; 0 failed.**

| Coverage | Result |
| --- | --- |
| Non-stream WAV download and returned binary | PASS; 10,284 bytes, valid WAV |
| Streaming PCM SSE and returned Base64 audio | PASS; decoded bytes match the fixture |
| Non-stream PCM aggregation | PASS; 10,240 bytes match the fixture |
| Dated snapshot and model mapping, stream/non-stream | PASS |
| Language selection and supported default speed | PASS |
| Upstream character usage, stream/non-stream | PASS; 37 characters, not upstream fixed-zero token counts |
| Missing usage Unicode fallback, stream/non-stream | PASS; 61 characters in the E2E input |
| Explicit zero usage, stream/non-stream | PASS; zero consumption |
| Ratio, per-call and expression pricing, stream/non-stream | PASS; actual consumption logs checked |
| HTTP error, malformed JSON, body error, negative usage, empty audio | PASS; no false successful consumption |
| Download 404, invalid WAV and invalid Base64 | PASS |
| Incomplete streaming and buffered responses | PASS; no fabricated `audio.done`; buffered mode returns 502 |
| Unsupported MP3, speed zero, instructions, missing voice, SSE WAV | PASS; HTTP 400 |
| SSRF protection and download authorization isolation | PASS; private URL rejected; no channel API key sent to download URL |
| Existing Ali chat stream/non-stream | PASS; content/reasoning and quota preserved |
| Existing OpenAI TTS | PASS; returned audio and character billing unchanged |
| Progressive downstream streaming | PASS; first chunk at 0.016 s, completion at 0.625 s |
| 20 simultaneous SSE/PCM requests | PASS; 20 distinct Request IDs, correct audio, exact quota delta |

A streaming failure after audio has already been sent retains HTTP 200 because
the headers are committed, but emits an error and never emits `audio.done`.
It is not classified as a successful completed audio response.

## Actual Billing Evidence

The test configures a ratio of 10 ($20 per 1M input characters), group ratio
0.65 and quota conversion of 500,000 quota per dollar.

| Scenario | Expected quota | Observed quota |
| --- | --- | --- |
| 37 characters, ratio pricing | round(37 * 10 * 0.65) = 241 | 241 |
| Per-call price $0.02 | 0.02 * 500000 * 0.65 = 6500 | 6500 |
| Expression `p * 20 + c * 0`, 37 characters | 241 | 241 |
| Explicit zero characters | 0 | 0 |
| 20 concurrent successful requests | 20 * 241 = 4820 | 4820 |
| Existing Ali chat: 100 input, 20 output, output ratio 2 | 91 | 91 |
| Existing OpenAI TTS: 18 characters, ratio 7.5 | 88 | 88 |

Example Request IDs:

- WAV/ratio: `202609171550192181458958268d9d6xTPCNytZ`.
- Per-call: `202609171550194119249748268d9d6YanazkRn`.
- Expression: `202609171550194867122468268d9d62evtnWtU`.
- Progressive stream: `202609171550214964672558268d9d6Ruvdf9Vu`.

## Unit Tests

Passed:

```sh
go test ./relay/channel/ali ./dto ./relay/channel/openai ./relay/channel -count=1
go test ./service -run '^(TestCalculateTextQuotaSummary|TestBuildTieredTokenParams|TestTryTieredSettle)' -count=1
go test ./relay/helper -run '^(TestModelPriceHelper|TestBufferedSSE)' -count=1
```

Running the broader `relay/helper` suite also found an existing failure in
`TestStreamScannerHandler_StreamStatus_PreInitialized`: expected error count 1,
observed 0. This exact failure was reproduced in a clean, detached worktree of
the base commit above. It is unrelated to this adapter and was not changed.
The full helper suite must therefore not be reported as passing.

## Artifacts and Limits

- Machine-readable result: `.test/qwen-tts-e2e/results/report.json`.
- The same directory contains saved request JSON, raw response audio/SSE,
  response metadata and actual NewAPI consumption log JSON for each request.
- Unit outputs: `.test/qwen-tts-e2e/{unit-tests,billing-unit-tests,price-unit-tests}.txt`.
- Reproduction commands and supported client parameters: [README](README.md).
- This verifies the official-format mock through the complete application,
  not a live DashScope account; no real DashScope key was supplied.
- The generic channel test button was not extended for TTS. Verify this model
  with an actual `/v1/audio/speech` request.
- No default model price was invented. Configure input price per character
  before use. MP3 transcoding, custom speed and realtime TTS are out of scope.
