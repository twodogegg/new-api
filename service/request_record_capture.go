package service

import (
	"bytes"
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const requestRecordContextKey = "request_record_capture"

// Captures share a process-wide memory budget, including active requests and
// queued writes. Exhausting it drops recording, never the relay itself.
var requestRecordMemory atomic.Int64

type requestRecordBuffer struct {
	mu         sync.Mutex
	data       []byte
	written    int64
	limit      int
	startedAt  time.Time
	compressed bool
	compressor *gzip.Writer
	truncated  bool
}

// requestRecordCompressedSink is called while the buffer lock is held.
type requestRecordCompressedSink struct{ buffer *requestRecordBuffer }

func (sink requestRecordCompressedSink) Write(data []byte) (int, error) {
	sink.buffer.store(data)
	return len(data), nil
}

func (b *requestRecordBuffer) store(data []byte) {
	n := min(len(data), b.limit-len(b.data))
	if n > 0 && requestRecordMemory.Add(int64(n)) <= 128<<20 {
		b.data = append(b.data, data[:n]...)
	} else if n > 0 {
		requestRecordMemory.Add(-int64(n))
		n = 0
	}
	if n != len(data) {
		b.truncated = true
	}
}

func (b *requestRecordBuffer) enableStream() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.written != 0 || b.compressed {
		return
	}
	b.compressed = true
	b.compressor = gzip.NewWriter(requestRecordCompressedSink{buffer: b})
}

func (b *requestRecordBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.written += int64(len(data))
	if b.compressed {
		if _, err := b.compressor.Write(data); err != nil {
			b.truncated = true
		}
	} else {
		b.store(data)
	}
	return len(data), nil
}

type requestRecordWriter struct {
	gin.ResponseWriter
	buffer *requestRecordBuffer
}

func (w *requestRecordWriter) Write(data []byte) (int, error) {
	if strings.HasPrefix(strings.ToLower(w.Header().Get("Content-Type")), "text/event-stream") {
		w.buffer.enableStream()
	}
	n, err := w.ResponseWriter.Write(data)
	_, _ = w.buffer.Write(data[:n])
	return n, err
}

func (w *requestRecordWriter) WriteString(data string) (int, error) {
	if strings.HasPrefix(strings.ToLower(w.Header().Get("Content-Type")), "text/event-stream") {
		w.buffer.enableStream()
	}
	n, err := w.ResponseWriter.WriteString(data)
	_, _ = w.buffer.Write([]byte(data[:n]))
	return n, err
}

func (w *requestRecordWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type requestRecordReader struct {
	io.ReadCloser
	buffer *requestRecordBuffer
}

func (r *requestRecordReader) Read(data []byte) (int, error) {
	n, err := r.ReadCloser.Read(data)
	_, _ = r.buffer.Write(data[:n])
	return n, err
}

type requestRecordCapture struct {
	mu        sync.Mutex
	record    model.RequestRecord
	client    *requestRecordBuffer
	snapshots []requestRecordRawSnapshot
	limit     int
}

type requestRecordRawSnapshot struct {
	Stage       string
	ContentType string
	Buffer      *requestRecordBuffer
	Attempt     int
	Status      int
}

// BeginRequestRecord starts before ingress plugins mutate the client body. Its
// returned completion callback must run after the final client response.
func BeginRequestRecord(c *gin.Context, format string) func() {
	settings := operation_setting.GetRequestRecordSetting()
	if !settings.Enabled || c.Request.Method != http.MethodPost || c.GetInt("id") <= 0 {
		return func() {}
	}
	if _, exists := c.Get(requestRecordContextKey); exists {
		return func() {}
	}
	limit := min(max(settings.MaxBodyMB, 1), 64) << 20
	started := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
	if started.IsZero() {
		started = time.Now()
	}
	capture := &requestRecordCapture{
		limit: limit,
		record: model.RequestRecord{
			UserID: c.GetInt("id"), RequestID: c.GetString(common.RequestIdKey),
			TokenID: c.GetInt("token_id"), RequestPath: c.Request.URL.Path,
			CreatedAt: started.Unix(), ExpiresAt: started.Unix() + int64(max(settings.RetentionDays, 1))*86400,
			NodeName: common.NodeName, Operation: "request", BodyState: "available",
			Kind: requestRecordKind(c.Request.URL.Path, format),
		},
		client: &requestRecordBuffer{limit: limit, startedAt: started},
	}
	if capture.record.RequestID == "" {
		capture.record.RequestID = common.NewRequestId()
		c.Set(common.RequestIdKey, capture.record.RequestID)
	}
	request := &requestRecordBuffer{limit: limit}
	if storage, err := common.GetBodyStorage(c); err == nil {
		if reader, err := storage.NewReader(); err == nil {
			_, _ = io.Copy(request, io.LimitReader(reader, int64(limit)+1))
			_ = reader.Close()
			request.written = storage.Size()
		}
	}
	capture.record.ConversationID = requestRecordAssociation(c.GetHeader("X-Conversation-Id"), request.data, "conversation_id", "metadata.conversation_id", "session_id", "metadata.session_id")
	capture.record.RunID = requestRecordAssociation(c.GetHeader("X-Agent-Run-Id"), request.data, "agent_run_id", "metadata.agent_run_id", "metadata.run_id")
	capture.snapshots = append(capture.snapshots, requestRecordRawSnapshot{Stage: "client_request", ContentType: c.GetHeader("Content-Type"), Buffer: request})
	c.Set(requestRecordContextKey, capture)
	c.Writer = &requestRecordWriter{ResponseWriter: c.Writer, buffer: capture.client}
	return func() {
		capture.record.ModelName = common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
		capture.record.ChannelID = c.GetInt("channel_id")
		capture.record.DurationMS = time.Since(started).Milliseconds()
		capture.record.Status = "success"
		if c.Writer.Status() >= 400 {
			capture.record.Status = "failed"
		} else if c.Request.Context().Err() != nil {
			capture.record.Status = "interrupted"
		}
		capture.mu.Lock()
		capture.snapshots = append(capture.snapshots, requestRecordRawSnapshot{Stage: "client_response", ContentType: c.Writer.Header().Get("Content-Type"), Buffer: capture.client, Status: c.Writer.Status()})
		job := capture.detach()
		capture.mu.Unlock()
		enqueueRequestRecord(job)
	}
}

func requestRecordKind(path, format string) string {
	path = strings.ToLower(path)
	switch {
	case strings.Contains(path, "image") || format == "openai_image" || format == "mj_proxy":
		return "image"
	case strings.Contains(path, "audio") || format == "openai_audio":
		return "audio"
	case strings.Contains(path, "video"):
		return "video"
	case format == "task":
		return "task"
	default:
		return "llm"
	}
}

func requestRecordAssociation(header string, body []byte, paths ...string) string {
	value := strings.TrimSpace(header)
	if value == "" {
		for _, path := range paths {
			if candidate := gjson.GetBytes(body, path); candidate.Type == gjson.String && candidate.Str != "" {
				value = candidate.Str
				break
			}
		}
	}
	if len(value) > 191 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

// CaptureRequestRecordUpstream reads only an independent replay of the final
// wire request. An unreplayable body remains explicitly uncaptured.
func CaptureRequestRecordUpstream(c *gin.Context, req *http.Request) func(*http.Response) {
	value, exists := c.Get(requestRecordContextKey)
	if !exists {
		return func(*http.Response) {}
	}
	capture := value.(*requestRecordCapture)
	capture.mu.Lock()
	if len(capture.snapshots) >= 18 {
		capture.record.BodyState = "truncated"
		capture.mu.Unlock()
		return func(*http.Response) {}
	}
	attempt := (len(capture.snapshots) - 1) / 2
	request := &requestRecordBuffer{limit: capture.limit}
	if req.GetBody != nil {
		if reader, err := req.GetBody(); err == nil {
			_, _ = io.Copy(request, io.LimitReader(reader, int64(capture.limit)+1))
			_ = reader.Close()
			if req.ContentLength > request.written {
				request.written = req.ContentLength
			}
		}
	}
	capture.snapshots = append(capture.snapshots, requestRecordRawSnapshot{Stage: "upstream_request", ContentType: req.Header.Get("Content-Type"), Buffer: request, Attempt: attempt})
	capture.mu.Unlock()
	return func(response *http.Response) {
		if response == nil || response.Body == nil {
			return
		}
		buffer := &requestRecordBuffer{limit: capture.limit}
		if strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
			buffer.enableStream()
		}
		capture.mu.Lock()
		capture.snapshots = append(capture.snapshots, requestRecordRawSnapshot{Stage: "upstream_response", ContentType: response.Header.Get("Content-Type"), Buffer: buffer, Attempt: attempt, Status: response.StatusCode})
		capture.mu.Unlock()
		response.Body = &requestRecordReader{ReadCloser: response.Body, buffer: buffer}
	}
}

func AttachRequestRecordTask(c *gin.Context, task *model.Task) {
	if value, exists := c.Get(requestRecordContextKey); exists {
		capture := value.(*requestRecordCapture)
		capture.record.TaskID = task.TaskID
		if capture.record.Kind == "task" {
			capture.record.Kind = requestRecordKind(task.Action, "task")
		}
	}
}

func (capture *requestRecordCapture) detach() *requestRecordJob {
	job := &requestRecordJob{record: capture.record}
	for _, raw := range capture.snapshots {
		raw.Buffer.mu.Lock()
		if raw.Buffer.compressor != nil {
			if err := raw.Buffer.compressor.Close(); err != nil {
				raw.Buffer.truncated = true
			}
		}
		// Ownership moves to the worker; no Gin context, reader or pooled body
		// storage escapes the request lifetime.
		data := bytes.Clone(raw.Buffer.data)
		job.bytes += int64(len(data))
		requestRecordMemory.Add(-int64(len(raw.Buffer.data)))
		raw.Buffer.data = nil
		truncated := raw.Buffer.truncated || (!raw.Buffer.compressed && raw.Buffer.written > int64(len(data)))
		compressed := raw.Buffer.compressed
		raw.Buffer.limit = 0
		raw.Buffer.mu.Unlock()
		contentType, params, _ := mime.ParseMediaType(raw.ContentType)
		job.snapshots = append(job.snapshots, requestRecordInput{Stage: raw.Stage, ContentType: contentType, Boundary: params["boundary"], Data: data, Truncated: truncated, Compressed: compressed, Attempt: raw.Attempt, Status: raw.Status})
	}
	return job
}
