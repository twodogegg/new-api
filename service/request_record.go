package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/tidwall/gjson"
)

type requestRecordJob struct {
	record    model.RequestRecord
	snapshots []requestRecordInput
	bytes     int64
}

var requestRecordJobs = struct {
	start   sync.Once
	pending sync.WaitGroup
	queue   chan *requestRecordJob
	dropped atomic.Int64
	warned  atomic.Int64
}{queue: make(chan *requestRecordJob, 64)}

func enqueueRequestRecord(job *requestRecordJob) {
	if requestRecordMemory.Add(job.bytes) > 128<<20 {
		requestRecordMemory.Add(-job.bytes)
		dropRequestRecording()
		return
	}
	requestRecordJobs.start.Do(func() {
		for range 2 {
			go func() {
				for job := range requestRecordJobs.queue {
					job.run()
					requestRecordJobs.pending.Done()
				}
			}()
		}
	})
	requestRecordJobs.pending.Add(1)
	select {
	case requestRecordJobs.queue <- job:
	default:
		requestRecordMemory.Add(-job.bytes)
		requestRecordJobs.pending.Done()
		dropRequestRecording()
	}
}

func dropRequestRecording() {
	requestRecordJobs.dropped.Add(1)
	if dropped := requestRecordJobs.dropped.Load(); dropped > 0 {
		now, previous := time.Now().Unix(), requestRecordJobs.warned.Load()
		if now-previous >= 60 && requestRecordJobs.warned.CompareAndSwap(previous, now) {
			common.SysError(fmt.Sprintf("request recording queue is full; %d records were not captured", requestRecordJobs.dropped.Swap(0)))
		}
	}
}

func FlushRequestRecords(ctx context.Context) error {
	done := make(chan struct{})
	go func() { requestRecordJobs.pending.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (job *requestRecordJob) run() {
	defer requestRecordMemory.Add(-job.bytes)
	defer func() {
		if recover() != nil {
			common.SysError("request recording failed: worker panicked")
		}
	}()
	if err := job.persist(); err != nil {
		// Payloads, URLs and credentials must never enter operational logs.
		common.SysError("request recording failed: unable to persist record " + job.record.RequestID)
		job.record.ID, job.record.Manifest, job.record.BodyState = 0, "", "unavailable"
		_ = model.SaveRequestRecord(&job.record, nil, nil)
	}
}

func (job *requestRecordJob) persist() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	settings := operation_setting.GetRequestRecordSetting()
	content := &requestRecordContent{
		ctx:    ctx,
		record: &job.record, limit: min(max(settings.MaxBodyMB, 1)*4, 128) << 20,
		blobs: make(map[string]model.RequestRecordBlob), files: make(map[string][]byte),
		manifest: requestRecordManifest{Snapshots: []RequestRecordSnapshot{}, Media: []RequestRecordMedia{}},
	}
	saved := false
	for _, input := range job.snapshots {
		body, state := content.decode(input)
		snapshot := RequestRecordSnapshot{Stage: input.Stage, ContentType: input.ContentType, Attempt: input.Attempt, Status: input.Status, State: state}
		if input.Truncated {
			snapshot.State = "truncated"
		}
		if snapshot.State == "truncated" {
			job.record.BodyState = "truncated"
		}
		if body != nil {
			packed, err := content.split(body)
			if err != nil {
				snapshot.State, job.record.BodyState = "truncated", "truncated"
			} else if encoded, err := common.Marshal(packed); err != nil {
				return err
			} else if id, err := content.addBlob(encoded, "application/json", requestRecordSearchText(packed)); err != nil {
				snapshot.State, job.record.BodyState = "truncated", "truncated"
			} else {
				snapshot.BlobID = id
				saved = true
			}
		}
		content.manifest.Snapshots = append(content.manifest.Snapshots, snapshot)
	}
	if !saved && job.record.BodyState == "available" {
		job.record.BodyState = "not_captured"
	}
	manifest, err := common.Marshal(content.manifest)
	if err != nil {
		return err
	}
	job.record.Manifest = string(manifest)
	return content.persist()
}

type RequestRecordDetail struct {
	Record    *model.RequestRecord    `json:"record"`
	Snapshots []RequestRecordSnapshot `json:"snapshots"`
	Media     []RequestRecordMedia    `json:"media"`
}

func LoadRequestRecordDetail(record *model.RequestRecord) (*RequestRecordDetail, error) {
	detail := &RequestRecordDetail{Record: record, Snapshots: []RequestRecordSnapshot{}, Media: []RequestRecordMedia{}}
	if model.RequestRecordExpired(record, requestRecordRetentionDays()) {
		record.BodyState = "expired"
		return detail, nil
	}
	if record.Manifest == "" {
		record.BodyState = "unavailable"
		return detail, nil
	}
	var manifest requestRecordManifest
	if err := common.UnmarshalJsonStr(record.Manifest, &manifest); err != nil {
		return nil, err
	}
	blobs, err := model.GetRequestRecordBlobs(record.ID)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(blobs))
	for _, blob := range blobs {
		allowed[blob.ID] = true
	}
	loader := requestRecordLoader{allowed: allowed, cache: make(map[string]any)}
	for _, snapshot := range manifest.Snapshots {
		if snapshot.BlobID != "" {
			body, err := loader.load(snapshot.BlobID, 0)
			if err != nil {
				snapshot.State = "unavailable"
				record.BodyState = "unavailable"
			} else {
				snapshot.Body = body
			}
		}
		detail.Snapshots = append(detail.Snapshots, snapshot)
	}
	detail.Media = manifest.Media
	return detail, nil
}

type requestRecordLoader struct {
	allowed map[string]bool
	cache   map[string]any
}

func (loader *requestRecordLoader) load(id string, depth int) (any, error) {
	if !loader.allowed[id] || depth > 128 {
		return nil, errors.New("invalid record reference")
	}
	if cached, exists := loader.cache[id]; exists {
		return cached, nil
	}
	path, err := RequestRecordBlobPath(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !gjson.ValidBytes(data) {
		return nil, errors.New("invalid stored JSON")
	}
	value := requestRecordJSONValue(gjson.ParseBytes(data))
	value, err = loader.expand(value, depth+1)
	if err == nil {
		loader.cache[id] = value
	}
	return value, err
}

// Keep protocol numbers exact while resolving references. Decoding into any
// through float64 would round identifiers above JavaScript's integer range.
func requestRecordJSONValue(value gjson.Result) any {
	if value.IsObject() {
		object := make(map[string]any)
		value.ForEach(func(key, child gjson.Result) bool { object[key.String()] = requestRecordJSONValue(child); return true })
		return object
	}
	if value.IsArray() {
		array := make([]any, 0)
		value.ForEach(func(_, child gjson.Result) bool { array = append(array, requestRecordJSONValue(child)); return true })
		return array
	}
	if value.Type == gjson.Number {
		return common.RawMessage(value.Raw)
	}
	return value.Value()
}

func (loader *requestRecordLoader) expand(value any, depth int) (any, error) {
	if depth > 128 {
		return nil, errors.New("record reference depth exceeded")
	}
	switch current := value.(type) {
	case map[string]any:
		if refs, ok := current["_record_events"].([]any); ok && len(current) == 1 {
			events := make([]any, 0)
			for _, ref := range refs {
				id, ok := ref.(string)
				if !ok {
					return nil, errors.New("invalid event reference")
				}
				chunk, err := loader.load(id, depth+1)
				if err != nil {
					return nil, err
				}
				items, ok := chunk.([]any)
				if !ok {
					return nil, errors.New("invalid event fragment")
				}
				events = append(events, items...)
			}
			return events, nil
		}
		if literal, ok := current["_record_literal"].(map[string]any); ok && len(current) == 1 {
			for key, child := range literal {
				expanded, err := loader.expand(child, depth+1)
				if err != nil {
					return nil, err
				}
				literal[key] = expanded
			}
			return literal, nil
		}
		if refs, ok := current["_record_text"].([]any); ok && len(current) == 1 {
			var text strings.Builder
			for _, ref := range refs {
				id, ok := ref.(string)
				if !ok {
					return nil, errors.New("invalid text reference")
				}
				value, err := loader.load(id, depth+1)
				if err != nil {
					return nil, err
				}
				part, ok := value.(string)
				if !ok {
					return nil, errors.New("invalid text fragment")
				}
				text.WriteString(part)
			}
			return text.String(), nil
		}
		if id, ok := current["_record_ref"].(string); ok && len(current) == 1 {
			return loader.load(id, depth+1)
		}
		for key, child := range current {
			expanded, err := loader.expand(child, depth+1)
			if err != nil {
				return nil, err
			}
			current[key] = expanded
		}
	case []any:
		for i, child := range current {
			expanded, err := loader.expand(child, depth+1)
			if err != nil {
				return nil, err
			}
			current[i] = expanded
		}
	}
	return value, nil
}

// RecordTaskCompletion records the durable task result independently of the
// submission response. Repeated terminal notifications are idempotent.
func RecordTaskCompletion(task *model.Task) {
	if !operation_setting.GetRequestRecordSetting().Enabled || task == nil || task.PrivateData.Execution == nil || task.PrivateData.Execution.RequestID == "" {
		return
	}
	parent := task.PrivateData.Execution.RequestID
	data := task.Data
	if len(data) > 64<<20 {
		data = nil
	}
	var result any
	if gjson.ValidBytes(data) {
		result = requestRecordJSONValue(gjson.ParseBytes(data))
	}
	body, err := common.Marshal(map[string]any{"task_id": task.TaskID, "status": task.Status, "progress": task.Progress, "reason": task.FailReason, "result_url": task.PrivateData.ResultURL, "result": result})
	if err != nil {
		return
	}
	finished := task.FinishTime
	if finished == 0 {
		finished = time.Now().Unix()
	}
	job := &requestRecordJob{
		record: model.RequestRecord{UserID: task.UserId, RequestID: parent + ":result", ParentID: parent, TaskID: task.TaskID,
			ModelName: task.Properties.OriginModelName, Kind: requestRecordKind(task.Action, "task"), Operation: "task_result",
			Status: string(task.Status), BodyState: "available", CreatedAt: finished, ExpiresAt: finished + int64(requestRecordRetentionDays())*86400, NodeName: common.NodeName},
		snapshots: []requestRecordInput{{Stage: "task_result", ContentType: "application/json", Data: body}}, bytes: int64(len(body)),
	}
	enqueueRequestRecord(job)
}

// RecordTaskPoll captures only this task's own query. Batch query envelopes are
// intentionally excluded because they may contain other users' tasks.
func RecordTaskPoll(task *model.Task, response *http.Response, body []byte, started time.Time) {
	settings := operation_setting.GetRequestRecordSetting()
	if !settings.Enabled || task == nil || task.PrivateData.Execution == nil || task.PrivateData.Execution.RequestID == "" {
		return
	}
	limit := min(max(settings.MaxBodyMB, 1), 64) << 20
	parent := task.PrivateData.Execution.RequestID
	job := &requestRecordJob{record: model.RequestRecord{
		UserID: task.UserId, RequestID: common.NewRequestId(), ParentID: parent, TaskID: task.TaskID,
		ModelName: task.Properties.OriginModelName, Kind: requestRecordKind(task.Action, "task"), Operation: "poll", Status: "failed",
		BodyState: "available", CreatedAt: started.Unix(), ExpiresAt: started.Unix() + int64(requestRecordRetentionDays())*86400,
		DurationMS: time.Since(started).Milliseconds(), NodeName: common.NodeName,
	}}
	if response != nil {
		job.record.Status = fmt.Sprint(response.StatusCode)
		if request := response.Request; request != nil {
			job.record.RequestPath = request.Method + " " + request.URL.Path
			if request.GetBody != nil {
				if reader, err := request.GetBody(); err == nil {
					data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
					_ = reader.Close()
					if err == nil {
						job.snapshots = append(job.snapshots, requestRecordInput{Stage: "upstream_request", ContentType: request.Header.Get("Content-Type"), Data: data[:min(len(data), limit)], Truncated: len(data) > limit})
					}
				}
			}
		}
		job.snapshots = append(job.snapshots, requestRecordInput{Stage: "upstream_response", ContentType: strings.Split(response.Header.Get("Content-Type"), ";")[0], Data: bytes.Clone(body[:min(len(body), limit)]), Truncated: len(body) > limit, Status: response.StatusCode})
	}
	for _, snapshot := range job.snapshots {
		job.bytes += int64(len(snapshot.Data))
	}
	enqueueRequestRecord(job)
}

// StartRequestRecordCleanup runs on every node: each node owns its local files,
// while database transactions coordinate expiry and shared-file cleanup.
func StartRequestRecordCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		var lastOrphanScan time.Time
		for {
			// Scan stale files before normal cleanup, adopting only hashes with
			// no committed database row. Fresh in-flight files are left alone.
			if time.Since(lastOrphanScan) >= time.Hour {
				lastOrphanScan = time.Now()
				_ = filepath.WalkDir(requestRecordDirectory(), func(path string, entry os.DirEntry, err error) error {
					if err != nil || entry.IsDir() {
						return nil
					}
					info, err := entry.Info()
					if err != nil || time.Since(info.ModTime()) < time.Hour {
						return nil
					}
					if strings.HasPrefix(entry.Name(), ".record-") {
						return os.Remove(path)
					}
					if _, err := RequestRecordBlobPath(entry.Name()); err == nil {
						return model.AdoptOrphanRequestRecordBlob(entry.Name(), common.NodeName)
					}
					return nil
				})
			}
			if err := model.ExpireRequestRecords(time.Now().Unix(), requestRecordRetentionDays()); err != nil {
				common.SysError("request recording cleanup failed: unable to expire records")
			} else if err := model.CleanupRequestRecordBlobs(common.NodeName, func(id string) error {
				path, err := RequestRecordBlobPath(id)
				if err != nil {
					return err
				}
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				return nil
			}); err != nil {
				common.SysError("request recording cleanup failed: unable to remove files")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// RequestRecordStorageUsage scans only private recording files. It never returns
// filesystem paths or the body contents to the dashboard.
func RequestRecordStorageUsage() (int64, error) {
	var total int64
	err := filepath.WalkDir(requestRecordDirectory(), func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		info, err := entry.Info()
		if err == nil {
			total += info.Size()
		}
		return err
	})
	return total, err
}
