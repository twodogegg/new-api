package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/tidwall/gjson"
)

type RequestRecordSnapshot struct {
	Stage       string `json:"stage"`
	ContentType string `json:"content_type"`
	Attempt     int    `json:"attempt"`
	Status      int    `json:"status,omitempty"`
	State       string `json:"state"`
	BlobID      string `json:"blob_id,omitempty"`
	Body        any    `json:"body,omitempty"`
}

type RequestRecordMedia struct {
	BlobID   string `json:"blob_id"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
	Stage    string `json:"stage"`
	Path     string `json:"path"`
}

type requestRecordManifest struct {
	Snapshots []RequestRecordSnapshot `json:"snapshots"`
	Media     []RequestRecordMedia    `json:"media"`
}

type requestRecordInput struct {
	Stage       string
	ContentType string
	Boundary    string
	Data        []byte
	Truncated   bool
	Attempt     int
	Status      int
}

type requestRecordContent struct {
	ctx      context.Context
	record   *model.RequestRecord
	blobs    map[string]model.RequestRecordBlob
	files    map[string][]byte
	manifest requestRecordManifest
	stage    string
	limit    int
	bytes    int
	nodes    int
}

func requestRecordDirectory() string {
	return common.GetEnvOrDefaultString("REQUEST_RECORD_DIR", filepath.Join("data", "request-records"))
}

func RequestRecordBlobPath(id string) (string, error) {
	if len(id) != 64 {
		return "", errors.New("invalid record file")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", errors.New("invalid record file")
	}
	return filepath.Join(requestRecordDirectory(), id[:2], id), nil
}

func (content *requestRecordContent) addBlob(data []byte, mimeType, search string) (string, error) {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%d\x00%s\x00%s\x00", content.record.UserID, content.record.NodeName, mimeType)
	_, _ = hash.Write(data)
	id := hex.EncodeToString(hash.Sum(nil))
	if _, exists := content.blobs[id]; exists {
		return id, nil
	}
	if content.bytes+len(data) > content.limit || len(content.blobs) >= 8192 {
		return "", errors.New("record storage limit exceeded")
	}
	content.bytes += len(data)
	content.files[id] = data
	content.blobs[id] = model.RequestRecordBlob{ID: id, NodeName: content.record.NodeName, MimeType: mimeType, Size: int64(len(data)), SearchText: search, CreatedAt: time.Now().Unix()}
	return id, nil
}

func (content *requestRecordContent) addMedia(data []byte, mimeType, path string) any {
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = http.DetectContentType(data)
	}
	id, err := content.addBlob(data, mimeType, "")
	if err != nil {
		content.record.BodyState = "truncated"
		return map[string]any{"_record_media": "", "state": "not_retained"}
	}
	content.manifest.Media = append(content.manifest.Media, RequestRecordMedia{BlobID: id, MimeType: mimeType, Size: int64(len(data)), Stage: content.stage, Path: path})
	return map[string]any{"_record_media": id, "mime_type": mimeType, "size": len(data)}
}

// normalize converts only the snapshot, never the request being relayed. Media
// is externalized before JSON is persisted and repeated JSON nodes share files.
func (content *requestRecordContent) normalize(value gjson.Result, path string, depth int) any {
	content.nodes++
	if depth > 64 || content.nodes > 65536 {
		content.record.BodyState = "truncated"
		return "[record truncated]"
	}
	if value.IsObject() {
		result := make(map[string]any)
		mimeType := value.Get("mimeType").String()
		if mimeType == "" {
			mimeType = value.Get("media_type").String()
		}
		if mimeType == "" {
			mimeType = value.Get("mime_type").String()
		}
		value.ForEach(func(key, child gjson.Result) bool {
			name := key.String()
			childPath := path + "/" + name
			if requestRecordSecret(name) {
				result[name] = "[redacted]"
			} else if child.Type == gjson.String && (name == "b64_json" || name == "bytesBase64Encoded" || name == "data" && (mimeType != "" || value.Get("type").String() == "base64" || strings.Contains(path, "/audio"))) {
				result[name] = content.decodeMedia(child.Str, mimeType, childPath)
			} else {
				result[name] = content.normalize(child, childPath, depth+1)
			}
			return true
		})
		for name := range result {
			if strings.HasPrefix(name, "_record_") {
				return map[string]any{"_record_literal": result}
			}
		}
		return result
	}
	if value.IsArray() {
		result := make([]any, 0)
		value.ForEach(func(index, child gjson.Result) bool {
			result = append(result, content.normalize(child, path+"/"+index.String(), depth+1))
			return true
		})
		return result
	}
	if value.Type == gjson.Number {
		// Preserve exact JSON numbers, including explicit zero and large ids.
		return common.RawMessage(value.Raw)
	}
	if value.Type != gjson.String {
		return value.Value()
	}
	text := value.Str
	if gjson.Valid(text) && (strings.HasPrefix(strings.TrimSpace(text), "{") || strings.HasPrefix(strings.TrimSpace(text), "[")) {
		if normalized, err := common.Marshal(content.normalize(gjson.Parse(text), path, depth+1)); err == nil {
			return string(normalized)
		}
	}
	if strings.HasPrefix(text, "data:") {
		header, encoded, found := strings.Cut(text, ",")
		if found && strings.HasSuffix(header, ";base64") {
			return content.decodeMedia(encoded, strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64"), path)
		}
		return "[unrecognized inline media]"
	}
	// Capture media URLs from structured media fields, not links embedded in
	// prompts, tool results or ordinary prose. Fetching uses the shared SSRF guard.
	// Plugin reference fields (input_reference/image/reference_image/...) carry a
	// bare URL string rather than a nested {url}, so treat those names as media.
	referenceField := slices.Contains(
		[]string{"input_reference", "image", "image_url", "reference_image", "first_frame_image", "last_frame_image"},
		strings.ToLower(path[strings.LastIndex(path, "/")+1:]),
	)
	isHTTPURL := strings.HasPrefix(text, "https://") || strings.HasPrefix(text, "http://")
	if referenceField && isHTTPURL {
		if media, err := content.downloadMedia(text, path); err == nil {
			return media
		}
		return map[string]any{"_record_media": "", "state": "unavailable", "source": requestRecordSafeURL(text)}
	}
	if (strings.HasSuffix(path, "/url") || strings.HasSuffix(path, "/image_url") || strings.HasSuffix(path, "/video_url") || strings.HasSuffix(path, "/audio_url") || strings.HasSuffix(path, "/result_url")) &&
		(strings.Contains(path, "/data/") || strings.Contains(path, "image_url") || strings.Contains(path, "video_url") || strings.Contains(path, "audio_url") || strings.Contains(path, "/video/") || strings.Contains(path, "/audio/") || strings.HasSuffix(path, "/result_url")) &&
		isHTTPURL {
		if media, err := content.downloadMedia(text, path); err == nil {
			return media
		}
		return map[string]any{"_record_media": "", "state": "unavailable", "source": requestRecordSafeURL(text)}
	}
	if strings.HasPrefix(text, "https://") || strings.HasPrefix(text, "http://") {
		text = requestRecordSafeURL(text)
	}
	return text
}

func requestRecordSecret(name string) bool {
	name = strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(name))
	switch name {
	case "authorization", "proxyauthorization", "cookie", "setcookie", "apikey", "xapikey", "xgoogapikey", "password", "passwd", "secret", "clientsecret", "accesskey", "secretkey", "privatekey", "accesstoken", "refreshtoken", "idtoken", "sessiontoken":
		return true
	}
	return false
}

func requestRecordSafeURL(text string) string {
	parsed, err := url.Parse(text)
	if err != nil {
		return "[invalid URL]"
	}
	parsed.User = nil
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if requestRecordSecret(key) || lower == "key" || lower == "token" || strings.Contains(lower, "signature") || strings.Contains(lower, "credential") {
			query.Set(key, "[redacted]")
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (content *requestRecordContent) decodeMedia(encoded, mimeType, path string) any {
	if base64.StdEncoding.DecodedLen(len(encoded)) > content.limit-content.bytes {
		content.record.BodyState = "truncated"
		return map[string]any{"_record_media": "", "state": "not_retained"}
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if data, err := encoding.DecodeString(encoded); err == nil {
			return content.addMedia(data, mimeType, path)
		}
	}
	return map[string]any{"_record_media": "", "state": "invalid"}
}

func (content *requestRecordContent) downloadMedia(address, path string) (any, error) {
	if len(content.manifest.Media) >= 32 {
		return nil, errors.New("too many media files")
	}
	ctx, cancel := context.WithTimeout(content.ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	client := GetSSRFProtectedHTTPClient()
	if client == nil {
		return nil, errors.New("fetch client is unavailable")
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("media is unavailable")
	}
	remaining := content.limit - content.bytes
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(remaining)+1))
	if err != nil || len(data) > remaining {
		return nil, errors.New("media exceeds recording limit")
	}
	return content.addMedia(data, strings.Split(response.Header.Get("Content-Type"), ";")[0], path), nil
}

func (content *requestRecordContent) decode(input requestRecordInput) (any, string) {
	content.stage = input.Stage
	if input.ContentType == "text/event-stream" {
		events := make([]any, 0)
		blocks := strings.Split(strings.ReplaceAll(string(input.Data), "\r\n", "\n"), "\n\n")
		if input.Truncated && len(blocks) > 0 {
			blocks = blocks[:len(blocks)-1]
		}
		for _, block := range blocks {
			if len(events) >= 10000 {
				content.record.BodyState = "truncated"
				break
			}
			var name string
			var data []string
			for line := range strings.SplitSeq(block, "\n") {
				field, value, _ := strings.Cut(line, ":")
				value = strings.TrimPrefix(value, " ")
				if field == "event" {
					name = value
				} else if field == "data" {
					data = append(data, value)
				}
			}
			payload := strings.Join(data, "\n")
			if payload == "" || payload == "[DONE]" {
				continue
			}
			if !gjson.Valid(payload) {
				continue
			}
			value := gjson.Parse(payload)
			if name == "error" || value.Get("error").Exists() || value.Get("type").String() == "error" {
				content.record.Status = "failed"
			}
			events = append(events, map[string]any{"event": name, "data": content.normalize(value, "/events/"+strconv.Itoa(len(events)), 0)})
		}
		return requestRecordMergedStream(events), "available"
	}
	if input.Truncated {
		return nil, "truncated"
	}
	if input.ContentType == "multipart/form-data" {
		fields := make(map[string]any)
		reader := multipart.NewReader(bytes.NewReader(input.Data), input.Boundary)
		for {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, "not_captured"
			}
			data, err := io.ReadAll(io.LimitReader(part, int64(content.limit)+1))
			_ = part.Close()
			if err != nil || len(data) > content.limit {
				return nil, "truncated"
			}
			var value any
			if requestRecordSecret(part.FormName()) {
				value = "[redacted]"
			} else if part.FileName() != "" {
				value = content.addMedia(data, part.Header.Get("Content-Type"), "/"+part.FormName())
			} else {
				encoded, err := common.Marshal(string(data))
				if err != nil {
					return nil, "not_captured"
				}
				value = content.normalize(gjson.ParseBytes(encoded), "/"+part.FormName(), 0)
			}
			name := part.FormName()
			if previous, exists := fields[name]; exists {
				if items, ok := previous.([]any); ok {
					fields[name] = append(items, value)
				} else {
					fields[name] = []any{previous, value}
				}
			} else {
				fields[name] = value
			}
		}
		return fields, "available"
	}
	if gjson.ValidBytes(input.Data) {
		value := gjson.ParseBytes(input.Data)
		if input.Stage == "client_response" && value.Get("error").Exists() {
			content.record.Status = "failed"
		}
		return content.normalize(value, "", 0), "available"
	}
	if strings.HasPrefix(input.ContentType, "image/") || strings.HasPrefix(input.ContentType, "video/") || strings.HasPrefix(input.ContentType, "audio/") {
		return content.addMedia(input.Data, input.ContentType, "/body"), "available"
	}
	if strings.HasPrefix(input.ContentType, "text/") && utf8.Valid(input.Data) {
		return string(input.Data), "available"
	}
	return nil, "not_captured"
}

type requestRecordStreamToolCall struct {
	id        string
	name      strings.Builder
	arguments strings.Builder
}

type requestRecordStreamChoice struct {
	index        int
	content      strings.Builder
	reasoning    strings.Builder
	finishReason string
	toolCalls    map[int]*requestRecordStreamToolCall
	toolOrder    []int
}

func (choice *requestRecordStreamChoice) toolCall(index int) *requestRecordStreamToolCall {
	if call, exists := choice.toolCalls[index]; exists {
		return call
	}
	call := &requestRecordStreamToolCall{}
	choice.toolCalls[index] = call
	choice.toolOrder = append(choice.toolOrder, index)
	return call
}

func requestRecordValueMap(value any) map[string]any {
	if object, ok := value.(map[string]any); ok {
		return object
	}
	return nil
}

func requestRecordValueList(value any) []any {
	if list, ok := value.([]any); ok {
		return list
	}
	return nil
}

func requestRecordValueString(value any) string {
	switch current := value.(type) {
	case string:
		return current
	case common.RawMessage:
		return string(current)
	case []byte:
		return string(current)
	case int:
		return strconv.Itoa(current)
	case int64:
		return strconv.FormatInt(current, 10)
	case float64:
		return strconv.FormatFloat(current, 'f', -1, 64)
	default:
		return ""
	}
}

func requestRecordValueInt(value any) int {
	text := requestRecordValueString(value)
	if text == "" {
		return 0
	}
	number, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0
	}
	return int(number)
}

// applyClaudeStreamEvent folds one Anthropic Messages stream event into the
// merged choice: text/thinking deltas append, and tool_use blocks accumulate
// their streamed JSON input keyed by content-block index.
func requestRecordApplyClaudeEvent(data map[string]any, choice *requestRecordStreamChoice) {
	switch requestRecordValueString(data["type"]) {
	case "content_block_start":
		block := requestRecordValueMap(data["content_block"])
		index := requestRecordValueInt(data["index"])
		switch requestRecordValueString(block["type"]) {
		case "text":
			choice.content.WriteString(requestRecordValueString(block["text"]))
		case "thinking":
			choice.reasoning.WriteString(requestRecordValueString(block["thinking"]))
		case "tool_use":
			call := choice.toolCall(index)
			call.id = requestRecordValueString(block["id"])
			call.name.WriteString(requestRecordValueString(block["name"]))
		}
	case "content_block_delta":
		delta := requestRecordValueMap(data["delta"])
		index := requestRecordValueInt(data["index"])
		switch requestRecordValueString(delta["type"]) {
		case "text_delta":
			choice.content.WriteString(requestRecordValueString(delta["text"]))
		case "thinking_delta":
			choice.reasoning.WriteString(requestRecordValueString(delta["thinking"]))
		case "input_json_delta":
			choice.toolCall(index).arguments.WriteString(requestRecordValueString(delta["partial_json"]))
		}
	case "message_delta":
		if reason := requestRecordValueString(requestRecordValueMap(data["delta"])["stop_reason"]); reason != "" {
			choice.finishReason = reason
		}
	}
}

// requestRecordMergedStream reduces a streamed response to a single readable
// result. Storing every delta would make a request record enormous, so the
// snapshot keeps the assembled assistant reply (content, reasoning and tool
// calls) and never the per-chunk wire frames.
func requestRecordMergedStream(events []any) any {
	// A completed Responses API event already carries the full response.
	for _, item := range events {
		data := requestRecordValueMap(requestRecordValueMap(item)["data"])
		switch requestRecordValueString(data["type"]) {
		case "response.completed", "response.incomplete":
			if response := requestRecordValueMap(data["response"]); response != nil {
				return response
			}
		}
	}
	choices := make(map[int]*requestRecordStreamChoice)
	order := make([]int, 0)
	getChoice := func(index int) *requestRecordStreamChoice {
		if choice, exists := choices[index]; exists {
			return choice
		}
		choice := &requestRecordStreamChoice{index: index, toolCalls: make(map[int]*requestRecordStreamToolCall)}
		choices[index] = choice
		order = append(order, index)
		return choice
	}
	var id, model string
	var usage any
	for _, item := range events {
		data := requestRecordValueMap(requestRecordValueMap(item)["data"])
		if data == nil {
			continue
		}
		if value := requestRecordValueString(data["id"]); value != "" {
			id = value
		}
		if value := requestRecordValueString(data["model"]); value != "" {
			model = value
		}
		if message := requestRecordValueMap(data["message"]); message != nil {
			if value := requestRecordValueString(message["model"]); value != "" {
				model = value
			}
			if value := requestRecordValueString(message["id"]); value != "" {
				id = value
			}
		}
		if value, exists := data["usage"]; exists && value != nil {
			usage = value
		}

		// OpenAI chat completions deltas.
		for _, rawChoice := range requestRecordValueList(data["choices"]) {
			choice := requestRecordValueMap(rawChoice)
			if choice == nil {
				continue
			}
			merged := getChoice(requestRecordValueInt(choice["index"]))
			delta := requestRecordValueMap(choice["delta"])
			if delta == nil {
				delta = requestRecordValueMap(choice["message"])
			}
			if delta != nil {
				merged.content.WriteString(requestRecordValueString(delta["content"]))
				merged.reasoning.WriteString(requestRecordValueString(delta["reasoning_content"]))
				merged.reasoning.WriteString(requestRecordValueString(delta["reasoning"]))
				for _, rawCall := range requestRecordValueList(delta["tool_calls"]) {
					call := requestRecordValueMap(rawCall)
					if call == nil {
						continue
					}
					mergedCall := merged.toolCall(requestRecordValueInt(call["index"]))
					if value := requestRecordValueString(call["id"]); value != "" {
						mergedCall.id = value
					}
					if function := requestRecordValueMap(call["function"]); function != nil {
						mergedCall.name.WriteString(requestRecordValueString(function["name"]))
						mergedCall.arguments.WriteString(requestRecordValueString(function["arguments"]))
					}
				}
			}
			if reason := requestRecordValueString(choice["finish_reason"]); reason != "" {
				merged.finishReason = reason
			}
		}

		// OpenAI Responses API deltas when no completed event exists.
		switch requestRecordValueString(data["type"]) {
		case "response.output_text.delta":
			getChoice(0).content.WriteString(requestRecordValueString(data["delta"]))
		case "response.reasoning_summary_text.delta":
			getChoice(0).reasoning.WriteString(requestRecordValueString(data["delta"]))
		}

		// Anthropic Messages deltas.
		requestRecordApplyClaudeEvent(data, getChoice(0))

		// Gemini candidate parts.
		for _, rawCandidate := range requestRecordValueList(data["candidates"]) {
			candidate := requestRecordValueMap(rawCandidate)
			if candidate == nil {
				continue
			}
			merged := getChoice(requestRecordValueInt(candidate["index"]))
			content := requestRecordValueMap(candidate["content"])
			for _, rawPart := range requestRecordValueList(content["parts"]) {
				part := requestRecordValueMap(rawPart)
				if part == nil {
					continue
				}
				text := requestRecordValueString(part["text"])
				if part["thought"] == true {
					merged.reasoning.WriteString(text)
				} else {
					merged.content.WriteString(text)
				}
				if function := requestRecordValueMap(part["functionCall"]); function != nil {
					call := merged.toolCall(len(merged.toolOrder))
					call.name.WriteString(requestRecordValueString(function["name"]))
					if encoded, err := common.Marshal(function["args"]); err == nil {
						call.arguments.WriteString(string(encoded))
					}
				}
			}
		}
	}
	mergedChoices := make([]any, 0, len(order))
	for _, index := range order {
		choice := choices[index]
		message := map[string]any{"role": "assistant", "content": choice.content.String()}
		if reasoning := choice.reasoning.String(); reasoning != "" {
			message["reasoning_content"] = reasoning
		}
		if len(choice.toolOrder) > 0 {
			calls := make([]any, 0, len(choice.toolOrder))
			for _, toolIndex := range choice.toolOrder {
				call := choice.toolCalls[toolIndex]
				calls = append(calls, map[string]any{"id": call.id, "type": "function", "function": map[string]any{"name": call.name.String(), "arguments": call.arguments.String()}})
			}
			message["tool_calls"] = calls
		}
		mergedChoices = append(mergedChoices, map[string]any{"index": index, "message": message, "finish_reason": choice.finishReason})
	}
	result := map[string]any{"object": "chat.completion", "choices": mergedChoices}
	if id != "" {
		result["id"] = id
	}
	if model != "" {
		result["model"] = model
	}
	if usage != nil {
		result["usage"] = usage
	}
	return result
}

// split stores each message/tool/content node once, so resubmitted history and
// the four views of a request share their unchanged content.
func (content *requestRecordContent) split(value any) (any, error) {
	switch current := value.(type) {
	case string:
		if len(current) <= 12<<10 {
			return value, nil
		}
		var refs []string
		for len(current) > 0 {
			length := min(len(current), 12<<10)
			for length < len(current) && !utf8.RuneStart(current[length]) {
				length--
			}
			encoded, err := common.Marshal(current[:length])
			if err != nil {
				return nil, err
			}
			search := strings.ToValidUTF8(current[:min(len(current), length+2048)], "")
			id, err := content.addBlob(encoded, "application/json", search)
			if err != nil {
				return nil, err
			}
			refs = append(refs, id)
			current = current[length:]
		}
		return map[string]any{"_record_text": refs}, nil
	case map[string]any:
		for key, child := range current {
			packed, err := content.split(child)
			if err != nil {
				return nil, err
			}
			current[key] = packed
		}
	case []any:
		for i, child := range current {
			packed, err := content.split(child)
			if err != nil {
				return nil, err
			}
			encoded, err := common.Marshal(packed)
			if err != nil {
				return nil, err
			}
			id, err := content.addBlob(encoded, "application/json", requestRecordSearchText(child))
			if err != nil {
				return nil, err
			}
			current[i] = map[string]any{"_record_ref": id}
		}
	}
	return value, nil
}

func requestRecordSearchText(value any) string {
	var text strings.Builder
	var visit func(any)
	visit = func(node any) {
		if text.Len() >= 48<<10 {
			return
		}
		switch current := node.(type) {
		case string:
			text.WriteString(current[:min(len(current), (48<<10)-text.Len())])
			text.WriteByte('\n')
		case map[string]any:
			if _, media := current["_record_media"]; media {
				return
			}
			for _, key := range slices.Sorted(maps.Keys(current)) {
				if key != "_record_ref" {
					visit(current[key])
				}
			}
		case []any:
			for _, child := range current {
				visit(child)
			}
		}
	}
	visit(value)
	return strings.ToValidUTF8(text.String(), "")
}

func (content *requestRecordContent) persist() error {
	var blobs []model.RequestRecordBlob
	for _, blob := range content.blobs {
		blobs = append(blobs, blob)
	}
	return model.SaveRequestRecord(content.record, blobs, func(blob model.RequestRecordBlob) error {
		path, err := RequestRecordBlobPath(blob.ID)
		if err != nil {
			return err
		}
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		file, err := os.CreateTemp(filepath.Dir(path), ".record-*")
		if err != nil {
			return err
		}
		defer os.Remove(file.Name())
		_, writeErr := file.Write(content.files[blob.ID])
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		return os.Rename(file.Name(), path)
	})
}

func requestRecordRetentionDays() int {
	return max(operation_setting.GetRequestRecordSetting().RetentionDays, 1)
}
