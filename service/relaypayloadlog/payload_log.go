package relaypayloadlog

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

const (
	ProtocolOpenAI    = "openai"
	ProtocolAnthropic = "anthropic"

	requestContextKey = "relay_payload_log_request"
)

type Config struct {
	Enabled         bool
	Dir             string
	QueueSize       int
	WriterWorkers   int
	MaxFileBytes    int64
	MaxAgeDays      int
	Compress        bool
	MaxEntryBytes   int64
	CleanupInterval time.Duration
	SampleRate      float64
}

type requestCapture struct {
	Protocol string
	Raw      []byte
	Meta     entryMeta
}

type rawEntry struct {
	Meta              entryMeta
	RequestRaw        []byte
	ResponseRaw       []byte
	ResponseChunks    []string
	ResponseTruncated bool
	Error             *ErrorInfo
}

type entryMeta struct {
	CreatedAt     time.Time
	RequestID     string
	Protocol      string
	Stream        bool
	Path          string
	Method        string
	Model         string
	UpstreamModel string
	ChannelID     int
	ChannelName   string
	RetryIndex    int
}

type ErrorInfo struct {
	StatusCode int    `json:"status_code,omitempty"`
	Type       string `json:"type,omitempty"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
}

type manager struct {
	cfg         Config
	queue       chan rawEntry
	stop        chan struct{}
	done        chan struct{}
	stopped     atomic.Bool
	workers     []*writerWorker
	wg          sync.WaitGroup
	dropped     atomic.Uint64
	lastDropLog atomic.Int64
}

type writerWorker struct {
	id       int
	manager  *manager
	file     *os.File
	filePath string
	fileDate string
	fileSize int64
}

type responseCaptureWriter struct {
	gin.ResponseWriter
	maxBytes  int64
	buf       []byte
	truncated bool
}

var (
	activeMu sync.RWMutex
	active   *manager
)

func InitFromEnv() {
	cfg := Config{
		Enabled:         common.GetEnvOrDefaultBool("PAYLOAD_LOG_ENABLED", false),
		Dir:             common.GetEnvOrDefaultString("PAYLOAD_LOG_DIR", ""),
		QueueSize:       common.GetEnvOrDefault("PAYLOAD_LOG_QUEUE_SIZE", 10000),
		WriterWorkers:   common.GetEnvOrDefault("PAYLOAD_LOG_WRITER_WORKERS", 8),
		MaxFileBytes:    int64(common.GetEnvOrDefault("PAYLOAD_LOG_MAX_FILE_MB", 512)) << 20,
		MaxAgeDays:      common.GetEnvOrDefault("PAYLOAD_LOG_MAX_AGE_DAYS", 30),
		Compress:        common.GetEnvOrDefaultBool("PAYLOAD_LOG_COMPRESS", true),
		MaxEntryBytes:   int64(common.GetEnvOrDefault("PAYLOAD_LOG_MAX_ENTRY_MB", 2)) << 20,
		CleanupInterval: time.Duration(common.GetEnvOrDefault("PAYLOAD_LOG_CLEANUP_INTERVAL_MINUTES", 60)) * time.Minute,
		SampleRate:      getEnvFloatOrDefault("PAYLOAD_LOG_SAMPLE_RATE", 0.01),
	}
	if cfg.Dir == "" {
		if common.LogDir != nil && *common.LogDir != "" {
			cfg.Dir = filepath.Join(*common.LogDir, "payload")
		} else {
			cfg.Dir = "./payload-logs"
		}
	}
	Init(cfg)
}

func Init(cfg Config) {
	Shutdown(context.Background())
	if !cfg.Enabled {
		return
	}
	normalizeConfig(&cfg)
	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		logger.LogError(context.Background(), fmt.Sprintf("payload log disabled: mkdir %s failed: %s", cfg.Dir, err.Error()))
		return
	}
	m := &manager{
		cfg:   cfg,
		queue: make(chan rawEntry, cfg.QueueSize),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	m.workers = make([]*writerWorker, cfg.WriterWorkers)
	for i := 0; i < cfg.WriterWorkers; i++ {
		m.workers[i] = &writerWorker{
			id:      i,
			manager: m,
		}
	}
	activeMu.Lock()
	active = m
	activeMu.Unlock()
	go m.run()
	logger.LogInfo(context.Background(), fmt.Sprintf("payload log enabled: dir=%s queue=%d workers=%d sample_rate=%.4f", cfg.Dir, cfg.QueueSize, cfg.WriterWorkers, cfg.SampleRate))
}

func Shutdown(ctx context.Context) {
	activeMu.Lock()
	m := active
	active = nil
	activeMu.Unlock()
	if m == nil || m.stopped.Swap(true) {
		return
	}
	close(m.stop)
	select {
	case <-m.done:
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
	}
}

func Enabled() bool {
	return current() != nil
}

func AttachRequest(c *gin.Context, info *relaycommon.RelayInfo, protocol string, requestBody []byte) bool {
	if current() == nil || c == nil || len(requestBody) == 0 {
		return false
	}
	c.Set(requestContextKey, requestCapture{
		Protocol: protocol,
		Raw:      requestBody,
		Meta:     buildMeta(c, info, protocol, false),
	})
	return true
}

func StartCapture(c *gin.Context, info *relaycommon.RelayInfo, protocol string, requestBody []byte) func(stream bool, errorInfo *ErrorInfo) bool {
	m := current()
	if m == nil || c == nil || len(requestBody) == 0 {
		return nil
	}
	if !m.shouldSample() {
		return nil
	}
	AttachRequest(c, info, protocol, requestBody)
	writer := &responseCaptureWriter{
		ResponseWriter: c.Writer,
		maxBytes:       m.cfg.MaxEntryBytes,
	}
	c.Writer = writer
	return func(stream bool, errorInfo *ErrorInfo) bool {
		responseBody, truncated := writer.Bytes()
		return SubmitResponse(c, info, protocol, stream, responseBody, errorInfo, truncated)
	}
}

func SubmitResponse(c *gin.Context, info *relaycommon.RelayInfo, protocol string, stream bool, responseBody []byte, errorInfo *ErrorInfo, responseTruncated ...bool) bool {
	m := current()
	if m == nil || c == nil {
		return false
	}
	capture, _ := c.Get(requestContextKey)
	req, _ := capture.(requestCapture)
	meta := buildMeta(c, info, protocol, stream)
	if req.Meta.RequestID != "" {
		meta.CreatedAt = req.Meta.CreatedAt
		if meta.RequestID == "" {
			meta.RequestID = req.Meta.RequestID
		}
		if meta.Path == "" {
			meta.Path = req.Meta.Path
		}
		if meta.Method == "" {
			meta.Method = req.Meta.Method
		}
		if meta.Model == "" {
			meta.Model = req.Meta.Model
		}
		meta.Stream = stream
	}
	meta.Protocol = protocol
	return m.submit(rawEntry{
		Meta:              meta,
		RequestRaw:        req.Raw,
		ResponseRaw:       responseBody,
		ResponseTruncated: len(responseTruncated) > 0 && responseTruncated[0],
		Error:             errorInfo,
	})
}

func SubmitStreamResponse(c *gin.Context, info *relaycommon.RelayInfo, protocol string, chunks []string) bool {
	m := current()
	if m == nil || c == nil {
		return false
	}
	capture, _ := c.Get(requestContextKey)
	req, _ := capture.(requestCapture)
	meta := buildMeta(c, info, protocol, true)
	if req.Meta.RequestID != "" {
		meta.CreatedAt = req.Meta.CreatedAt
		if meta.RequestID == "" {
			meta.RequestID = req.Meta.RequestID
		}
		if meta.Path == "" {
			meta.Path = req.Meta.Path
		}
		if meta.Method == "" {
			meta.Method = req.Meta.Method
		}
		if meta.Model == "" {
			meta.Model = req.Meta.Model
		}
		meta.Stream = true
	}
	meta.Protocol = protocol
	return m.submit(rawEntry{
		Meta:           meta,
		RequestRaw:     req.Raw,
		ResponseChunks: append([]string(nil), chunks...),
	})
}

func normalizeConfig(cfg *Config) {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 10000
	}
	if cfg.WriterWorkers <= 0 {
		cfg.WriterWorkers = 8
	}
	if cfg.MaxFileBytes <= 0 {
		cfg.MaxFileBytes = 512 << 20
	}
	if cfg.MaxAgeDays <= 0 {
		cfg.MaxAgeDays = 30
	}
	if cfg.MaxEntryBytes <= 0 {
		cfg.MaxEntryBytes = 2 << 20
	}
	if cfg.CleanupInterval <= 0 {
		cfg.CleanupInterval = time.Hour
	}
	if cfg.SampleRate < 0 {
		cfg.SampleRate = 0
	}
	if cfg.SampleRate > 1 {
		cfg.SampleRate = 1
	}
}

func current() *manager {
	activeMu.RLock()
	m := active
	activeMu.RUnlock()
	if m == nil || m.stopped.Load() {
		return nil
	}
	return m
}

func buildMeta(c *gin.Context, info *relaycommon.RelayInfo, protocol string, stream bool) entryMeta {
	meta := entryMeta{
		CreatedAt: time.Now(),
		Protocol:  protocol,
		Stream:    stream,
	}
	if c != nil {
		meta.RequestID = c.GetString(common.RequestIdKey)
		if c.Request != nil {
			meta.Path = c.Request.URL.Path
			meta.Method = c.Request.Method
		}
	}
	if info != nil {
		meta.Model = info.OriginModelName
		meta.RetryIndex = info.RetryIndex
		if info.ChannelMeta != nil {
			meta.UpstreamModel = info.UpstreamModelName
			meta.ChannelID = info.ChannelId
			meta.ChannelName = info.ChannelName
		}
		if meta.RequestID == "" {
			meta.RequestID = info.RequestId
		}
	}
	return meta
}

func (m *manager) submit(entry rawEntry) bool {
	if m == nil || m.stopped.Load() {
		return false
	}
	select {
	case m.queue <- entry:
		return true
	default:
		m.logDrop()
		return false
	}
}

func (m *manager) shouldSample() bool {
	if m == nil {
		return false
	}
	if m.cfg.SampleRate >= 1 {
		return true
	}
	if m.cfg.SampleRate <= 0 {
		return false
	}
	return rand.Float64() < m.cfg.SampleRate
}

func getEnvFloatOrDefault(env string, defaultValue float64) float64 {
	value := strings.TrimSpace(os.Getenv(env))
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return defaultValue
	}
	return parsed
}

func (w *responseCaptureWriter) Write(data []byte) (int, error) {
	w.capture(data)
	return w.ResponseWriter.Write(data)
}

func (w *responseCaptureWriter) WriteString(data string) (int, error) {
	w.capture(common.StringToByteSlice(data))
	return w.ResponseWriter.WriteString(data)
}

func (w *responseCaptureWriter) capture(data []byte) {
	if w == nil || len(data) == 0 || w.truncated {
		return
	}
	remaining := int(w.maxBytes) - len(w.buf)
	if remaining <= 0 {
		w.truncated = true
		return
	}
	if len(data) > remaining {
		w.buf = append(w.buf, data[:remaining]...)
		w.truncated = true
		return
	}
	w.buf = append(w.buf, data...)
}

func (w *responseCaptureWriter) Bytes() ([]byte, bool) {
	if w == nil || len(w.buf) == 0 {
		return nil, false
	}
	return append([]byte(nil), w.buf...), w.truncated
}

func (m *manager) run() {
	defer close(m.done)
	m.cleanup()

	for _, worker := range m.workers {
		m.wg.Add(1)
		go worker.run()
	}

	ticker := time.NewTicker(m.cfg.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.cleanup()
		case <-m.stop:
			m.wg.Wait()
			return
		}
	}
}

func (w *writerWorker) run() {
	defer w.manager.wg.Done()
	defer w.closeFile()

	for {
		select {
		case entry := <-w.manager.queue:
			w.writeEntry(entry)
		case <-w.manager.stop:
			w.drain()
			return
		}
	}
}

func (w *writerWorker) drain() {
	for {
		select {
		case entry := <-w.manager.queue:
			w.writeEntry(entry)
		default:
			return
		}
	}
}

func (w *writerWorker) writeEntry(entry rawEntry) {
	record := map[string]any{
		"ts":             entry.Meta.CreatedAt.Format(time.RFC3339Nano),
		"request_id":     entry.Meta.RequestID,
		"protocol":       entry.Meta.Protocol,
		"stream":         entry.Meta.Stream,
		"method":         entry.Meta.Method,
		"path":           entry.Meta.Path,
		"model":          entry.Meta.Model,
		"upstream_model": entry.Meta.UpstreamModel,
		"channel_id":     entry.Meta.ChannelID,
		"channel_name":   entry.Meta.ChannelName,
		"retry_index":    entry.Meta.RetryIndex,
		"request":        sanitizePayload(entry.RequestRaw),
	}
	if entry.Error != nil {
		record["error"] = entry.Error
	}
	if entry.Meta.Stream && len(entry.ResponseChunks) > 0 {
		record["response"] = sanitizeStreamChunks(entry.ResponseChunks)
	} else if entry.Meta.Stream {
		record["response"] = sanitizeStreamBody(entry.ResponseRaw)
	} else {
		record["response"] = sanitizePayload(entry.ResponseRaw)
	}
	if entry.ResponseTruncated {
		record["response_truncated"] = true
	}

	line, err := common.Marshal(record)
	if err != nil {
		logger.LogError(context.Background(), "payload log marshal failed: "+err.Error())
		return
	}
	if int64(len(line)) > w.manager.cfg.MaxEntryBytes {
		record["truncated"] = true
		record["request"] = summarizePayload(entry.RequestRaw, "request")
		if entry.Meta.Stream {
			record["response"] = map[string]any{
				"type":          "sse",
				"chunks":        summarizeChunks(entry.ResponseChunks),
				"truncated":     true,
				"raw_chunk_num": len(entry.ResponseChunks),
			}
		} else {
			record["response"] = summarizePayload(entry.ResponseRaw, "response")
		}
		line, err = common.Marshal(record)
		if err != nil {
			logger.LogError(context.Background(), "payload log marshal truncated entry failed: "+err.Error())
			return
		}
	}
	if err := w.writeLine(line); err != nil {
		logger.LogError(context.Background(), "payload log write failed: "+err.Error())
	}
}

func (w *writerWorker) writeLine(line []byte) error {
	if err := w.ensureFile(int64(len(line) + 1)); err != nil {
		return err
	}
	n, err := w.file.Write(append(line, '\n'))
	w.fileSize += int64(n)
	return err
}

func (w *writerWorker) ensureFile(nextBytes int64) error {
	now := time.Now()
	date := now.Format("20060102")
	if w.file != nil && w.fileDate == date && w.fileSize+nextBytes <= w.manager.cfg.MaxFileBytes {
		return nil
	}
	w.rotate()
	name := fmt.Sprintf("payload-%s-%s-%09d-worker-%02d.jsonl", date, now.Format("150405"), now.Nanosecond(), w.id)
	path := filepath.Join(w.manager.cfg.Dir, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	w.file = file
	w.filePath = path
	w.fileDate = date
	if stat, statErr := file.Stat(); statErr == nil {
		w.fileSize = stat.Size()
	} else {
		w.fileSize = 0
	}
	return nil
}

func (w *writerWorker) rotate() {
	if w.file == nil {
		return
	}
	oldPath := w.filePath
	_ = w.file.Close()
	w.file = nil
	w.filePath = ""
	w.fileSize = 0
	if w.manager.cfg.Compress && oldPath != "" && strings.HasSuffix(oldPath, ".jsonl") {
		go compressLogFile(oldPath)
	}
}

func (w *writerWorker) closeFile() {
	if w.file == nil {
		return
	}
	_ = w.file.Close()
	w.file = nil
}

func (m *manager) cleanup() {
	if m.cfg.MaxAgeDays <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(m.cfg.MaxAgeDays) * 24 * time.Hour)
	matches, err := filepath.Glob(filepath.Join(m.cfg.Dir, "payload-*.jsonl*"))
	if err != nil {
		return
	}
	for _, path := range matches {
		stat, statErr := os.Stat(path)
		if statErr != nil || stat.IsDir() || stat.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(path)
	}
}

func (m *manager) logDrop() {
	dropped := m.dropped.Add(1)
	now := time.Now().Unix()
	last := m.lastDropLog.Load()
	if now-last < 10 || !m.lastDropLog.CompareAndSwap(last, now) {
		return
	}
	logger.LogWarn(context.Background(), fmt.Sprintf("payload log queue full, dropped=%d", dropped))
}

func compressLogFile(path string) {
	src, err := os.Open(path)
	if err != nil {
		return
	}
	defer src.Close()

	tmpPath := path + ".gz.tmp"
	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	gw := gzip.NewWriter(dst)
	_, copyErr := io.Copy(gw, src)
	closeErr := gw.Close()
	fileCloseErr := dst.Close()
	if copyErr != nil || closeErr != nil || fileCloseErr != nil {
		_ = os.Remove(tmpPath)
		return
	}
	if err := os.Rename(tmpPath, path+".gz"); err != nil {
		_ = os.Remove(tmpPath)
		return
	}
	_ = os.Remove(path)
}
