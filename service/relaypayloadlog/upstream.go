package relaypayloadlog

import (
	"io"
	"mime"
	"net/http"
	"sync"

	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

type requestCapture struct {
	manager  *manager
	entry    capturedEntry
	upstream *upstreamBody
}

type upstreamBody struct {
	io.ReadCloser
	entry    capturedEntry
	maxBytes int
	mu       sync.Mutex
	buf      []byte
	read     int
	finished bool
}

func StartCapture(c *gin.Context, info *relaycommon.RelayInfo, protocol string, loadRequestBody func() ([]byte, error)) func(*ErrorInfo) bool {
	m := current()
	if m == nil || c == nil || loadRequestBody == nil || !m.shouldSample() {
		return nil
	}
	requestBody, err := loadRequestBody()
	if err != nil {
		logger.LogWarn(c, "payload log request read skipped: "+err.Error())
		return nil
	}
	if len(requestBody) == 0 {
		return nil
	}
	capture := &requestCapture{
		manager: m,
		entry: capturedEntry{
			Meta:            buildMeta(c, info, protocol, info != nil && info.IsStream),
			Request:         sanitizePayload(requestBody),
			RequestRawBytes: len(requestBody),
		},
	}
	c.Set(requestContextKey, capture)
	return func(localError *ErrorInfo) bool {
		return capture.submit(c, true, localError)
	}
}

// CaptureUpstreamResponse observes reads already made by the relay, without
// buffering before the handler or draining the upstream on its behalf.
func CaptureUpstreamResponse(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) {
	value, ok := c.Get(requestContextKey)
	if !ok || resp == nil || resp.Body == nil {
		return
	}
	capture := value.(*requestCapture)
	if capture.upstream != nil {
		capture.submit(c, false, nil)
	}

	request := capture.entry.Meta
	meta := buildMeta(c, info, request.Protocol, request.Stream)
	meta.CreatedAt = request.CreatedAt
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	body := &upstreamBody{
		ReadCloser: resp.Body,
		maxBytes:   int(capture.manager.cfg.MaxEntryBytes),
		entry: capturedEntry{
			Meta:               meta,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  upstreamRequestID(resp.Header),
			UpstreamStream:     mediaType == "text/event-stream",
		},
	}
	capture.upstream = body
	resp.Body = body
}

func (capture *requestCapture) submit(c *gin.Context, final bool, localError *ErrorInfo) bool {
	entry := capture.entry
	if body := capture.upstream; body != nil {
		entry = body.takeEntry()
		capture.upstream = nil
		if final {
			entry.Request = capture.entry.Request
			entry.RequestRawBytes = capture.entry.RequestRawBytes
		}
	} else {
		// There is no upstream response to save for a locally generated error.
		entry.Error = localError
	}
	line, err := capture.manager.prepareLine(entry)
	if err != nil {
		logger.LogError(c, "payload log prepare failed: "+err.Error())
		return false
	}
	return capture.manager.submit(line)
}

func (b *upstreamBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.mu.Lock()
		if !b.finished {
			b.read += n
			keep := min(n, b.maxBytes-len(b.buf))
			b.buf = append(b.buf, p[:keep]...)
		}
		b.mu.Unlock()
	}
	return n, err
}

func (b *upstreamBody) takeEntry() capturedEntry {
	b.mu.Lock()
	b.finished = true
	data, read := b.buf, b.read
	b.buf = nil
	b.mu.Unlock()

	entry := b.entry
	entry.ResponseRawBytes = read
	entry.ResponseTruncated = read > len(data)
	switch {
	case entry.UpstreamStream && len(data) > 0:
		entry.Response = sanitizeStreamBody(data)
	case entry.ResponseTruncated:
		// A partial JSON object cannot be reliably scrubbed of media fields.
		entry.Response = summarizePayloadBytes(read, "response")
	default:
		entry.Response = sanitizePayload(data)
	}
	return entry
}

func upstreamRequestID(header http.Header) string {
	for _, key := range []string{"X-Request-Id", "Request-Id", "X-Tt-Logid", "X-Oneapi-Request-Id"} {
		if id := header.Get(key); id != "" {
			return truncateMetadata(id)
		}
	}
	return ""
}
