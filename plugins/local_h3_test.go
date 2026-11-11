package plugins_test

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalH3GatewayVideoProtocol(t *testing.T) {
	source, err := os.ReadFile("custom/local-h3/plugin.js")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().Register(string(source), jsplugin.Options{Key: "local-h3"})
	require.NoError(t, err)
	assert.Contains(t, plugin.Meta.Upstreams, "new_api")

	decoded, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
		"model": "h3-nsfw-opt", "body": map[string]any{"kind": "json", "value": map[string]any{
			"model": "h3-nsfw-opt", "duration": 5, "resolution": "768P", "ratio": "9:16", "enable_continuation": false,
			"content": []any{map[string]any{"type": "text", "text": "hello"}, map[string]any{"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "https://example.com/image.png"}}},
		}},
	})
	require.NoError(t, err)
	intent := alibabaObject(t, decoded)
	request := intent["requestBody"]
	for _, tc := range []struct {
		name, kind, path, model string
	}{
		{"gateway", "new_api", "/v1/videos", "h3-nsfw-opt"},
		{"vendor", "vendor", "/v2/video_generation", "h3-nsfw"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := map[string]any{"baseUrl": "https://example.com/", "apiKey": "token", "upstream": map[string]any{"kind": tc.kind}, "requestBody": request}
			result, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
			require.NoError(t, err)
			descriptor := alibabaObject(t, result)
			assert.Equal(t, "https://example.com"+tc.path, descriptor["url"])
			assert.Equal(t, "Bearer token", descriptor["headers"].(map[string]any)["Authorization"])
			body := descriptor["body"].(map[string]any)
			assert.Equal(t, tc.model, body["model"])
			assert.Equal(t, float64(5), body["duration"])
			assert.Equal(t, "768P", body["resolution"])
			assert.Equal(t, "9:16", body["ratio"])
			assert.Equal(t, false, body["enable_continuation"])
			assert.Len(t, body["content"], 2)
		})
	}
	usage, err := plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{"requestBody": request})
	require.NoError(t, err)
	assert.Equal(t, float64(5), alibabaObject(t, usage)["seconds"])
	for _, duration := range []any{0, 16, 4.5} {
		_, err = plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
			"model": "h3-nsfw", "body": map[string]any{"kind": "json", "value": map[string]any{"prompt": "hello", "duration": duration}},
		})
		assert.Error(t, err)
	}

	result, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{}, map[string]any{"body": map[string]any{"id": "task_upstream"}})
	require.NoError(t, err)
	assert.Equal(t, "task_upstream", alibabaObject(t, result)["taskId"])
	ctx := map[string]any{"baseUrl": "https://example.com", "apiKey": "token", "taskId": "task_upstream", "upstreamTaskId": "task_upstream", "artifactKey": "video", "clientRequest": map[string]any{"method": "GET"}, "upstream": map[string]any{"kind": "new_api"}}
	result, err = plugin.Engine.Call(t.Context(), "buildQueryRequest", ctx)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/v1/videos/task_upstream", alibabaObject(t, result)["url"])
	result, err = plugin.Engine.Call(t.Context(), "buildContentRequest", ctx)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/v1/videos/task_upstream/content", alibabaObject(t, result)["url"])
	for _, tc := range []struct{ status, expected string }{{"queued", "QUEUED"}, {"processing", "IN_PROGRESS"}, {"succeeded", "SUCCESS"}, {"failed", "FAILURE"}, {"unexpected", "UNKNOWN"}} {
		result, err = plugin.Engine.Call(t.Context(), "parseTaskResult", ctx, map[string]any{"status": tc.status, "progress": "50%", "error": map[string]any{"message": "failure"}})
		require.NoError(t, err)
		parsed := alibabaObject(t, result)
		assert.Equal(t, tc.expected, parsed["status"])
		assert.Equal(t, "50%", parsed["progress"])
	}
	result, err = plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{}, map[string]any{"task": map[string]any{"status": "succeeded", "progress": map[string]any{"percent": 100}}})
	require.NoError(t, err)
	assert.Equal(t, "SUCCESS", alibabaObject(t, result)["status"])
}

func TestLocalH3UsesUpstreamUsageForLegacyOrigin(t *testing.T) {
	source, err := os.ReadFile("custom/local-h3/plugin.js")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().Register(string(source), jsplugin.Options{Key: "local-h3"})
	require.NoError(t, err)
	ctx := jsplugin.WithFetcher(t.Context(), func(_ context.Context, request jsplugin.FetchRequest) (jsplugin.FetchResponse, error) {
		assert.Equal(t, "https://example.com/v2/query/video_generation/private-id", request.URL)
		assert.Equal(t, "Bearer test", request.Headers["Authorization"])
		return jsplugin.FetchResponse{Status: http.StatusOK, Body: []byte(`{"task":{"duration":7,"resolution":"480P"}}`)}, nil
	})
	value, err := plugin.Engine.Call(ctx, "extractUsage", map[string]any{
		"baseUrl": "https://example.com", "apiKey": "test", "action": "secondSampling", "model": "h3-nsfw",
		"requestBody": map[string]any{"size": "768P"},
		"originTasks": []any{map[string]any{"upstreamTaskId": "private-id", "status": "SUCCESS", "usageFacts": map[string]any{"seconds": 7}}},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"seconds": float64(7), "resolution": "768P"}, alibabaObject(t, value))
}

func TestLocalH3ContinuationAndSecondSampling(t *testing.T) {
	source, err := os.ReadFile("custom/local-h3/plugin.js")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().Register(string(source), jsplugin.Options{Key: "local-h3"})
	require.NoError(t, err)
	for _, size := range []string{"576P", "2K"} {
		_, err = plugin.Engine.CallPath(t.Context(), "native", []string{"secondSampling"}, map[string]any{"params": map[string]any{"task_id": "task_public"}, "body": map[string]any{"kind": "json", "value": map[string]any{"model": "h3-nsfw", "size": size}}})
		assert.Error(t, err, "unsupported size %s", size)
	}

	for _, tc := range []struct {
		name, action, path string
		body               map[string]any
		wantBody           map[string]any
		seconds            float64
		size               string
	}{
		{"continuation", "continuation", "/continue", map[string]any{"model": "h3-nsfw", "prompt": "continue", "seconds": 6}, map[string]any{"duration": float64(6), "reference_mode": "inherit", "content": []any{map[string]any{"type": "text", "text": "continue"}}}, 6, "480P"},
		{"secondSampling", "secondSampling", "/second-sampling", map[string]any{"model": "h3-nsfw", "size": "1080P"}, map[string]any{"target_resolution": "1080P"}, 5, "1080P"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := plugin.Engine.CallPath(t.Context(), "native", []string{tc.name}, map[string]any{"params": map[string]any{"task_id": "task_public"}, "body": map[string]any{"kind": "json", "value": tc.body}})
			require.NoError(t, err)
			intent := alibabaObject(t, value)
			assert.Equal(t, []any{"task_public"}, intent["originTaskIds"])
			ctx := map[string]any{"baseUrl": "https://example.com", "apiKey": "test", "action": tc.action, "model": "h3-nsfw", "requestBody": intent["requestBody"], "originTasks": []any{map[string]any{"taskId": "task_public", "upstreamTaskId": "private-id", "model": "h3-nsfw", "status": "SUCCESS", "usageFacts": map[string]any{"seconds": 5, "resolution": "480P"}}}}
			value, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
			require.NoError(t, err)
			descriptor := alibabaObject(t, value)
			assert.Equal(t, "https://example.com/v2/video_generation/private-id"+tc.path, descriptor["url"])
			assert.Equal(t, tc.wantBody, descriptor["body"])
			value, err = plugin.Engine.Call(t.Context(), "extractUsage", ctx)
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"seconds": tc.seconds, "resolution": tc.size}, alibabaObject(t, value))
			ctx["originTasks"] = []any{map[string]any{"upstreamTaskId": "private-id", "status": "QUEUED"}}
			_, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
			assert.Error(t, err)
		})
	}
}
