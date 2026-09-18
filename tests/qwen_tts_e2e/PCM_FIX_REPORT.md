# Qwen TTS PCM Header Fix

## Change

The real audio from channel 430 includes a RIFF/WAV header in its first SSE
audio chunk. The previous adapter returned those bytes unchanged while declaring
PCM. The fix removes the WAV envelope from both non-stream PCM and downstream
SSE, leaving the sample bytes unchanged.

- Raw PCM still passes through unchanged.
- WAV header parsing is incremental across SSE chunks, supports extra header
  chunks and odd-byte padding, and does not assume a fixed 44-byte header.
- The real provider's placeholder RIFF/data lengths are accepted; the existing
  SSE terminal event remains required.
- Header buffering is bounded to 64 KiB and released when sample data starts.
- Non-PCM WAV formats, oversized/incomplete headers and header-only responses
  return errors instead of false successful audio completion.
- Non-stream WAV download and all usage/pricing calculations are unchanged.

## Verification

Executed on 2026-09-18 at approximately 21:20 Beijing time.

The complete local Docker Compose stack contains the modified NewAPI executable,
PostgreSQL 15, Redis 7 and the Python mock. Channels and pricing are configured
through the admin API. The container binary matches the working-tree Linux/amd64
build, SHA-256:

```text
53c42449b7788941f74d804332fb842090651e44f4f24e9d0182d17ea6227818
```

**62 E2E cases passed, 0 failed:** 60 self-contained protocol/format/regression
cases plus 2 replay cases using the saved real channel-430 audio.

| Coverage | Result |
| --- | --- |
| WAV-wrapped SSE, streaming placeholder sizes | PASS; returned bytes are headerless PCM |
| Raw PCM, stream/non-stream | PASS; samples unchanged |
| Finite WAV, stream/non-stream | PASS |
| Header split across multiple SSE chunks, stream/non-stream | PASS |
| Metadata with odd-byte padding and extended fmt header | PASS |
| Invalid encoding, huge/incomplete header, header-only audio | PASS; no successful audio.done or consumption log |
| Captured channel-430 audio, stream/non-stream | PASS; exact sample-byte equality |
| Existing WAV, model mapping and snapshots | PASS |
| Character, per-call, expression, missing/zero usage billing | PASS; real consumption logs checked |
| Existing malformed/error/truncated response and SSRF cases | PASS |
| Existing Ali chat stream/non-stream and OpenAI TTS | PASS |
| Progressive streaming | PASS; first audio at 0.016 s, completion at 0.656 s |
| 20 concurrent mixed raw/WAV, stream/non-stream requests | PASS; exact quota delta 4820 |

## Real Audio Replay Evidence

Source: saved SSE from live channel 430, Request ID
`202609181311076598135568268d9d696VF5BY4`.

Its 10 decoded audio chunks originally total 153644 bytes, including the 44-byte
WAV header. The local mock repackages those exact audio chunks into native
DashScope response events, preserving the recorded character usage of 45.
Python's WAV decoder independently supplies the expected sample bytes.

- Non-stream PCM: Request ID `202609181320191112660068268d9d6mnl5MP46`;
  exactly 153600 output bytes, identical to the source WAV's sample payload.
- SSE PCM: Request ID `202609181320191517350278268d9d6bJaesq8o`;
  decoded audio exactly matches the same 153600 bytes; normal audio.done.
- Both saved consumption logs retain 45 characters, zero output units, group
  ratio 0.65. Local test model ratio 10 gives round(45 * 10 * 0.65) = 293 quota,
  matching both logs. The production channel's selling price was not changed.

This is replay of captured real audio through the fixed local service, not a new
live-provider inference or a production deployment.

## Unit Tests and Artifacts

All of these commands passed:

```sh
go test ./relay/channel/ali ./dto ./relay/channel/openai ./relay/channel -count=1
go test ./service -run '^(TestCalculateTextQuotaSummary|TestBuildTieredTokenParams|TestTryTieredSettle)' -count=1
go test ./relay/helper -run '^(TestModelPriceHelper|TestBufferedSSE)' -count=1
```

The new unit tests cover one-byte header fragmentation, raw short chunks,
placeholder sizes, format validation and actual downstream PCM/SSE output.
The previously documented unrelated full-helper-suite failure was not modified;
only the focused helper tests above are reported as passing.

- Local application: `http://127.0.0.1:13009`.
- Report JSON: `.test/qwen-tts-e2e/pcm-fix-results/report.json`.
- Requests, responses and actual billing log JSON: the same directory.
- Reproduction: [README](README.md). Without a recorded fixture, the suite has
  60 cases and needs no production credentials or data.

No production configuration, image or container was changed, and no production
service was restarted or upgraded.
