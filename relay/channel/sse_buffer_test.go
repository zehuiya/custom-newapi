package channel

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBufferedSSEIdleTimeout(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 1
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{}
	err := ConsumeBufferedSSE(c, &http.Response{Body: reader}, info, func(string) (bool, error) { return false, nil })
	require.EqualError(t, err, "buffered stream timed out after 1s")
	require.Equal(t, relaycommon.StreamEndReasonTimeout, info.StreamStatus.EndReason)
}

func TestBufferedSSEHeartbeatResetsIdleTimeout(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 1
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer writer.Close()
		for i := 0; i < 3; i++ {
			time.Sleep(500 * time.Millisecond)
			if _, err := io.WriteString(writer, ": heartbeat\n\n"); err != nil {
				return
			}
		}
		_, _ = io.WriteString(writer, "data: [DONE]\n\n")
	}()
	info := &relaycommon.RelayInfo{}
	err := ConsumeBufferedSSE(c, &http.Response{Body: reader}, info, func(string) (bool, error) { return false, nil })
	<-writerDone
	require.NoError(t, err)
	require.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
}

func TestBufferedSSEClientCancellation(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = constant.DefaultStreamingTimeout
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	info := &relaycommon.RelayInfo{}
	err := ConsumeBufferedSSE(c, &http.Response{Body: reader}, info, func(string) (bool, error) { return false, nil })
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
}
