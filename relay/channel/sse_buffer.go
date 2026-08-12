package channel

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/gin-gonic/gin"
)

type bufferedScanResult struct {
	line string
	err  error
	done bool
}

// ConsumeBufferedSSE reads upstream SSE without writing stream headers or
// chunks to the downstream response. The callback may stop after a terminal
// protocol event, allowing the caller to emit one non-stream JSON response.
func ConsumeBufferedSSE(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, handle func(data string) (bool, error)) error {
	if resp == nil || resp.Body == nil || handle == nil {
		return fmt.Errorf("invalid buffered stream response")
	}
	defer resp.Body.Close()

	info.StreamStatus = relaycommon.NewStreamStatus()
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	results := make(chan bufferedScanResult, 16)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		maxBufferSize := helper.DefaultMaxScannerBufferSize
		if constant.StreamScannerMaxBufferMB > 0 {
			maxBufferSize = constant.StreamScannerMaxBufferMB << 20
		}
		scanner.Buffer(make([]byte, helper.InitialScannerBufferSize), maxBufferSize)
		for scanner.Scan() {
			select {
			case results <- bufferedScanResult{line: scanner.Text()}:
			case <-ctx.Done():
				return
			}
		}
		result := bufferedScanResult{done: true, err: scanner.Err()}
		select {
		case results <- result:
		case <-ctx.Done():
		}
	}()

	timeout := time.Duration(constant.StreamingTimeout) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(timeout)
	}

	for {
		select {
		case result := <-results:
			if result.done {
				if result.err != nil && result.err != io.EOF {
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, result.err)
					return result.err
				}
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
				return nil
			}
			resetTimer()
			line := strings.TrimSpace(result.line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
				return nil
			}

			info.SetFirstResponseTime()
			info.ReceivedResponseCount++
			done, err := handle(data)
			if err != nil {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonHandlerStop, err)
				return err
			}
			if done {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
				return nil
			}
		case <-timer.C:
			err := fmt.Errorf("buffered stream timed out after %s", timeout)
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonTimeout, err)
			_ = resp.Body.Close()
			return err
		case <-c.Request.Context().Done():
			err := c.Request.Context().Err()
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
			_ = resp.Body.Close()
			return err
		}
	}
}

func BufferedJSONResponse(resp *http.Response, body []byte) *http.Response {
	header := make(http.Header)
	statusCode := http.StatusOK
	if resp != nil {
		header = resp.Header.Clone()
		statusCode = resp.StatusCode
	}
	header.Del("Content-Length")
	header.Del("Transfer-Encoding")
	header.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: statusCode,
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func IsEventStreamResponse(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	contentType := resp.Header.Get("Content-Type")
	if separator := strings.IndexByte(contentType, ';'); separator >= 0 {
		contentType = contentType[:separator]
	}
	return strings.EqualFold(strings.TrimSpace(contentType), "text/event-stream")
}
