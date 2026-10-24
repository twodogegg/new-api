package plugins_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relay/channel"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runningHubPlugin(t *testing.T) *jsplugin.LoadedPlugin {
	t.Helper()
	source, err := builtinplugins.Source("runninghub")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{})
	require.NoError(t, err)
	return plugin
}

func runningHubHook(t *testing.T, plugin *jsplugin.LoadedPlugin, path []string, args ...any) map[string]any {
	t.Helper()
	value, err := plugin.Engine.CallPath(t.Context(), path[0], path[1:], args...)
	require.NoError(t, err)
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, common.Unmarshal(encoded, &result))
	return result
}

func TestRunningHubWorkflowAndUsage(t *testing.T) {
	plugin := runningHubPlugin(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "runninghub-test-key", ChannelBaseUrl: "https://www.runninghub.cn", UpstreamModelName: "minimax-h3-flash"},
		OriginModelName: "minimax-h3-flash",
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
	}
	adaptor := taskplugin.New(plugin)
	adaptor.Init(info)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	c.Set("task_request", map[string]any{
		"model": "minimax-h3-flash", "prompt": "a cat", "seconds": "10", "size": "1080x1920",
		"input_reference": "https://cdn.example/cat.png",
		"metadata": map[string]any{
			"seed": 0, "instance_type": "ultra", "use_personal_queue": true,
			"reference_video": []any{"api/clip.mp4"}, "reference_audio": "openapi/voice.wav",
			// Unrecognized passthrough cannot rewrite the graph or the reserved quantity.
			"duration": 999999, "parameters": map[string]any{"seconds": 999999},
		},
	})
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	facts, err := adaptor.ExtractUsageFactsValidated(c, info)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"seconds": 243.0 / 24, "resolution": "1080P"}, facts)

	bodyReader, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	body, err := io.ReadAll(bodyReader)
	require.NoError(t, err)
	var request struct {
		APIKey       string `json:"apiKey"`
		WorkflowID   string `json:"workflowId"`
		Workflow     string `json:"workflow"`
		NodeInfoList []struct {
			NodeID     string `json:"nodeId"`
			FieldName  string `json:"fieldName"`
			FieldValue int    `json:"fieldValue"`
		} `json:"nodeInfoList"`
	}
	require.NoError(t, common.Unmarshal(body, &request))
	assert.Equal(t, "runninghub-test-key", request.APIKey)
	assert.Equal(t, "2113713138997743617", request.WorkflowID)
	var upstreamBody map[string]any
	require.NoError(t, common.Unmarshal(body, &upstreamBody))
	assert.Equal(t, "default", upstreamBody["instanceType"])
	assert.NotContains(t, upstreamBody, "usePersonalQueue")
	require.Len(t, request.NodeInfoList, 1)
	assert.Equal(t, "233", request.NodeInfoList[0].NodeID)
	assert.Equal(t, "seed", request.NodeInfoList[0].FieldName)
	assert.Positive(t, request.NodeInfoList[0].FieldValue, "the plugin generates its own seed")

	var workflow map[string]struct {
		Type   string         `json:"class_type"`
		Inputs map[string]any `json:"inputs"`
	}
	require.NoError(t, common.UnmarshalJsonStr(request.Workflow, &workflow))
	assert.Equal(t, "a cat", workflow["2"].Inputs["value"])
	assert.Equal(t, float64(10), workflow["17"].Inputs["value"])
	assert.Equal(t, float64(1088), workflow["11"].Inputs["width"])
	assert.Equal(t, float64(1920), workflow["11"].Inputs["height"])
	assert.Equal(t, float64(1080), workflow["322"].Inputs["width"])
	assert.Equal(t, float64(1920), workflow["322"].Inputs["height"])
	assert.Equal(t, float64(4), workflow["322"].Inputs["x"])
	assert.Equal(t, []any{"322", float64(0)}, workflow["243"].Inputs["images"])
	assert.Equal(t, float64(request.NodeInfoList[0].FieldValue), workflow["233"].Inputs["seed"])
	assert.Equal(t, "https://cdn.example/cat.png", workflow["274"].Inputs["image"])
	assert.Equal(t, "api/clip.mp4", workflow["301"].Inputs["video"])
	assert.Equal(t, "openapi/voice.wav", workflow["310"].Inputs["audio"])
	assert.Equal(t, float64(120), workflow["301"].Inputs["frame_load_cap"])
	for _, node := range workflow {
		for _, input := range node.Inputs {
			if link, ok := input.([]any); ok && len(link) == 2 {
				id, valid := link[0].(string)
				require.True(t, valid)
				assert.Contains(t, workflow, id, "every active reference must resolve to an attached node")
			}
		}
	}
	assert.NotContains(t, workflow["11"].Inputs, "ref_images.ref_image_1")
	assert.NotContains(t, workflow, "277", "unused example.png inputs must not execute")
	assert.NotContains(t, workflow, "313", "unused audio inputs must not execute")

	t.Run("text-only removes every optional reference", func(t *testing.T) {
		descriptor := runningHubHook(t, plugin, []string{"buildSubmitRequest"}, map[string]any{
			"model": "minimax-h3-flash", "upstreamModel": "minimax-h3-flash", "baseUrl": "https://www.runninghub.cn/",
			"apiKey": "test-key", "publicTaskId": "task-public",
			"requestBody": map[string]any{"prompt": "a cat"},
		})
		assert.Equal(t, "https://www.runninghub.cn/task/openapi/create", descriptor["url"])
		body := descriptor["body"].(map[string]any)
		var graph map[string]struct {
			Inputs map[string]any `json:"inputs"`
		}
		require.NoError(t, common.UnmarshalJsonStr(body["workflow"].(string), &graph))
		for key := range graph["11"].Inputs {
			assert.False(t, strings.HasPrefix(key, "ref_images.") || strings.HasPrefix(key, "ref_videos.") || strings.HasPrefix(key, "ref_audios."), key)
		}
	})
}

func TestRunningHubResolutionOutputs(t *testing.T) {
	plugin := runningHubPlugin(t)
	for _, tc := range []struct {
		size       string
		width      int
		height     int
		resolution string
	}{
		{"480P", 854, 480, "480P"},
		{"480x854", 480, 854, "480P"},
		{"480x480", 480, 480, "480P"},
		{"768P", 1344, 768, "768P"},
		{"768x1344", 768, 1344, "768P"},
		{"768x768", 768, 768, "768P"},
		{"1080P", 1920, 1080, "1080P"},
		{"1080x1920", 1080, 1920, "1080P"},
		{"1080x1080", 1080, 1080, "1080P"},
	} {
		t.Run(tc.size, func(t *testing.T) {
			ctx := map[string]any{"model": "minimax-h3-flash", "baseUrl": "https://www.runninghub.cn", "apiKey": "test-key",
				"requestBody": map[string]any{"prompt": "a cat", "size": tc.size},
			}
			descriptor := runningHubHook(t, plugin, []string{"buildSubmitRequest"}, ctx)
			body := descriptor["body"].(map[string]any)
			instance := "default"
			if tc.resolution == "768P" {
				instance = "plus"
			}
			assert.Equal(t, instance, body["instanceType"], "every output orientation must use its resolution's configured instance")
			var graph map[string]struct {
				Type   string         `json:"class_type"`
				Inputs map[string]any `json:"inputs"`
			}
			require.NoError(t, common.UnmarshalJsonStr(body["workflow"].(string), &graph))
			canvas := graph["11"].Inputs
			assert.Equal(t, float64((tc.width+31)/32*32), canvas["width"])
			assert.Equal(t, float64((tc.height+31)/32*32), canvas["height"])
			if tc.width%32 != 0 || tc.height%32 != 0 {
				crop := graph["322"]
				assert.Equal(t, "ImageCrop", crop.Type)
				assert.Equal(t, float64(tc.width), crop.Inputs["width"])
				assert.Equal(t, float64(tc.height), crop.Inputs["height"])
				assert.Equal(t, []any{"322", float64(0)}, graph["243"].Inputs["images"])
			} else {
				assert.NotContains(t, graph, "322")
			}
			facts := runningHubHook(t, plugin, []string{"extractUsage"}, ctx)
			assert.Equal(t, tc.resolution, facts["resolution"], "pricing must follow the requested output, not the padded canvas")
		})
	}
}

func TestRunningHubVideoDecoderValidation(t *testing.T) {
	plugin := runningHubPlugin(t)
	decode := func(kind string, body map[string]any) (any, error) {
		body["kind"] = kind
		return plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
			"model": "local-alias", "upstreamModel": "minimax-h3-flash", "body": body,
		})
	}
	for _, tc := range []struct {
		name string
		body map[string]any
		want string
	}{
		{"no prompt", map[string]any{}, "prompt is required"},
		{"zero", map[string]any{"seconds": 0}, "seconds must be"},
		{"negative", map[string]any{"seconds": -1}, "seconds must be"},
		{"overflow", map[string]any{"seconds": "18446744073709551615"}, "seconds must be"},
		{"fraction", map[string]any{"seconds": 5.5}, "seconds must be"},
		{"boolean", map[string]any{"seconds": true}, "seconds must be"},
		{"conflicting duration", map[string]any{"seconds": 5, "duration": 15}, "must match"},
		{"unsupported 720P", map[string]any{"size": "1280x720"}, "size must be"},
		{"unsupported 2K", map[string]any{"size": "2K"}, "size must be"},
		{"oversized canvas", map[string]any{"size": "8192x8192"}, "size must be"},
		{"wrong metadata", map[string]any{"metadata": []any{}}, "metadata must be an object"},
		{"invalid image", map[string]any{"input_reference": true}, "must contain non-empty"},
		{"local path", map[string]any{"input_reference": "/tmp/cat.png"}, "requires a public"},
		{"path traversal", map[string]any{"input_reference": "api/../cat.png"}, "requires a public"},
		{"base64", map[string]any{"input_reference": "data:image/png;base64,YQ=="}, "requires a public"},
		{"too many images", map[string]any{"images": []any{"api/1.png", "api/2.png", "api/3.png", "api/4.png", "api/5.png", "api/6.png", "api/7.png", "api/8.png", "api/9.png", "api/10.png"}}, "at most 9"},
		{"mixed frame and reference", map[string]any{"input_reference": "api/ref.png", "metadata": map[string]any{"last_frame_image": "api/cat.png"}}, "cannot mix"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := map[string]any{"prompt": "a cat"}
			if tc.name == "no prompt" {
				delete(request, "prompt")
			}
			for key, value := range tc.body {
				request[key] = value
			}
			_, err := decode("json", map[string]any{"value": request})
			require.ErrorContains(t, err, tc.want)
		})
	}
	t.Run("multipart preserves alias and references while dropping retired options", func(t *testing.T) {
		result, err := decode("multipart", map[string]any{"fields": map[string]any{
			"prompt": []string{"a cat"}, "seconds": []string{"5"},
			"metadata": []string{`{"seed":0,"instance_type":"plus","use_personal_queue":true,"reference_video":"api/clip.mp4"}`},
		}})
		require.NoError(t, err)
		encoded, err := common.Marshal(result)
		require.NoError(t, err)
		var intent struct {
			Model       string `json:"model"`
			RequestBody struct {
				Seconds  int            `json:"seconds"`
				Metadata map[string]any `json:"metadata"`
			} `json:"requestBody"`
		}
		require.NoError(t, common.Unmarshal(encoded, &intent))
		assert.Equal(t, "local-alias", intent.Model)
		assert.Equal(t, 5, intent.RequestBody.Seconds)
		assert.NotContains(t, intent.RequestBody.Metadata, "seed")
		assert.NotContains(t, intent.RequestBody.Metadata, "instance_type")
		assert.NotContains(t, intent.RequestBody.Metadata, "use_personal_queue")
		assert.Equal(t, []any{"api/clip.mp4"}, intent.RequestBody.Metadata["reference_video"])
	})
	t.Run("uploaded multipart files are rejected before submission", func(t *testing.T) {
		_, err := decode("multipart", map[string]any{"fields": map[string]any{"prompt": []string{"a cat"}}, "files": []any{map[string]any{"field": "input_reference", "ref": "request_file:input_reference"}}})
		require.ErrorContains(t, err, "Upload reference files to RunningHub first")
	})
	t.Run("malformed multipart metadata", func(t *testing.T) {
		_, err := decode("multipart", map[string]any{"fields": map[string]any{"prompt": []string{"a cat"}, "metadata": []string{"{"}}})
		require.ErrorContains(t, err, "metadata must be a JSON object string")
	})
	t.Run("duplicate multipart duration", func(t *testing.T) {
		_, err := decode("multipart", map[string]any{"fields": map[string]any{"prompt": []string{"a cat"}, "seconds": []string{"5", "15"}}})
		require.ErrorContains(t, err, "seconds must be provided once")
	})
}

func TestRunningHubStandardWorkflowAndLimits(t *testing.T) {
	plugin := runningHubPlugin(t)
	const standard = "minimax-h3-standard"
	assert.Contains(t, plugin.Meta.Models, standard)
	schema, examples := plugin.Meta.UsageForModel(standard)
	assert.Equal(t, []string{"480P", "768P"}, schema["resolution"].Enum)
	require.Len(t, examples, 2)
	flashSchema, _ := plugin.Meta.UsageForModel("minimax-h3-flash")
	assert.Equal(t, []string{"480P", "768P", "1080P"}, flashSchema["resolution"].Enum)
	t.Run("declared variants cannot be remapped silently", func(t *testing.T) {
		_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{
			"model": "minimax-h3-flash", "upstreamModel": standard, "baseUrl": "https://www.runninghub.cn",
			"apiKey": "test-key", "requestBody": map[string]any{"prompt": "a cat", "size": "480P"},
		})
		require.ErrorContains(t, err, "cannot switch Flash and Standard variants")
	})

	for _, size := range []string{"480P", "480x854", "480x480", "768P", "768x1344", "768x768", "1080P", "1920x1080", "1080x1920", "1080x1080"} {
		t.Run(size, func(t *testing.T) {
			request := map[string]any{"prompt": "a cat", "seconds": 15, "size": size,
				"input_reference": "api/cat.png", "metadata": map[string]any{
					"reference_video": "api/clip.mp4", "reference_audio": "openapi/voice.wav",
					"seed": 123, "instance_type": "plus", "use_personal_queue": true,
				},
			}
			ctx := map[string]any{"model": "standard-alias", "upstreamModel": standard, "baseUrl": "https://www.runninghub.cn",
				"apiKey": "test-key", "publicTaskId": "task_standard", "requestBody": request,
			}
			if strings.Contains(size, "1080") {
				for _, hook := range []string{"buildSubmitRequest", "extractUsage"} {
					_, err := plugin.Engine.Call(t.Context(), hook, ctx)
					require.ErrorContains(t, err, "supports only 480P and 768P")
				}
				for _, body := range []map[string]any{
					{"kind": "json", "value": request},
					{"kind": "multipart", "fields": map[string]any{"prompt": []string{"a cat"}, "size": []string{size}}},
				} {
					_, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
						"model": "standard-alias", "upstreamModel": standard, "body": body,
					})
					require.ErrorContains(t, err, "supports only 480P and 768P")
				}
				return
			}
			intent := runningHubHook(t, plugin, []string{"protocols", "openai_video", "decodeRequest"}, map[string]any{
				"model": "standard-alias", "upstreamModel": standard, "body": map[string]any{"kind": "json", "value": request},
			})
			assert.Equal(t, "standard-alias", intent["model"])
			ctx["requestBody"] = intent["requestBody"]
			descriptor := runningHubHook(t, plugin, []string{"buildSubmitRequest"}, ctx)
			body := descriptor["body"].(map[string]any)
			assert.Equal(t, "2113713138997743617", body["workflowId"])
			instance := "default"
			if strings.Contains(size, "768") {
				instance = "plus"
			}
			assert.Equal(t, instance, body["instanceType"])
			assert.NotContains(t, body, "usePersonalQueue")
			var graph map[string]struct {
				Type   string         `json:"class_type"`
				Inputs map[string]any `json:"inputs"`
			}
			require.NoError(t, common.UnmarshalJsonStr(body["workflow"].(string), &graph))
			assert.Equal(t, "a cat", graph["2"].Inputs["value"])
			assert.Equal(t, 15.0, graph["17"].Inputs["value"])
			assert.Equal(t, 35.0, graph["1124"].Inputs["steps"])
			assert.Equal(t, "SamplerCustomAdvanced", graph["1125"].Type)
			assert.Equal(t, "api/cat.png", graph["274"].Inputs["image"])
			assert.Equal(t, "api/clip.mp4", graph["301"].Inputs["video"])
			assert.Equal(t, "openapi/voice.wav", graph["310"].Inputs["audio"])
			assert.NotContains(t, graph, "277", "unused example media must not execute")
			assert.NotContains(t, graph, "233", "the standard workflow must not run the Flash sampler")
			for _, node := range graph {
				for _, input := range node.Inputs {
					if link, ok := input.([]any); ok && len(link) == 2 {
						assert.Contains(t, graph, link[0], "every graph link must resolve")
					}
				}
			}
			seed := body["nodeInfoList"].([]any)[0].(map[string]any)
			assert.Equal(t, "1129", seed["nodeId"])
			assert.Equal(t, "noise_seed", seed["fieldName"])
			assert.Equal(t, graph["1129"].Inputs["noise_seed"], seed["fieldValue"])
			assert.NotEqual(t, 123.0, seed["fieldValue"], "retired client seed must not affect the sampler")
			resolution := "480P"
			if strings.Contains(size, "768") {
				resolution = "768P"
			}
			assert.Equal(t, map[string]any{"seconds": 362.0 / 24, "resolution": resolution}, runningHubHook(t, plugin, []string{"extractUsage"}, ctx))
		})
	}

	t.Run("standard text-only has no sample media", func(t *testing.T) {
		descriptor := runningHubHook(t, plugin, []string{"buildSubmitRequest"}, map[string]any{
			"model": standard, "baseUrl": "https://www.runninghub.cn", "apiKey": "test-key", "publicTaskId": "task_text",
			"requestBody": map[string]any{"prompt": "a cat", "size": "768P"},
		})
		body := descriptor["body"].(map[string]any)
		var graph map[string]struct {
			Type   string         `json:"class_type"`
			Inputs map[string]any `json:"inputs"`
		}
		require.NoError(t, common.UnmarshalJsonStr(body["workflow"].(string), &graph))
		for key := range graph["1136"].Inputs {
			assert.False(t, strings.HasPrefix(key, "ref_images.") || strings.HasPrefix(key, "ref_videos.") || strings.HasPrefix(key, "ref_audios."), key)
		}
		assert.NotContains(t, body["workflow"].(string), "UPLOAD_")
		assert.Equal(t, 1344.0, graph["1136"].Inputs["width"])
		assert.Equal(t, 768.0, graph["1136"].Inputs["height"])
	})
}

func TestRunningHubFirstLastFramesAndMiniMaxAPI(t *testing.T) {
	plugin := runningHubPlugin(t)
	for _, modelName := range []string{"minimax-h3-flash", "minimax-h3-standard"} {
		for _, frames := range []map[string]any{
			{"first_frame_image": "api/start.png"},
			{"last_frame_image": "openapi/end.png"},
			{"first_frame_image": "api/start.png", "last_frame_image": "openapi/end.png"},
		} {
			t.Run(modelName+"/frames", func(t *testing.T) {
				request := map[string]any{"prompt": "a cat walks to the window", "seconds": 4, "size": "768P", "metadata": frames}
				metadataJSON, err := common.Marshal(frames)
				require.NoError(t, err)
				for _, body := range []map[string]any{
					{"kind": "json", "value": request},
					{"kind": "multipart", "fields": map[string]any{
						"prompt": []string{"a cat walks to the window"}, "seconds": []string{"4"}, "size": []string{"768P"},
						"metadata": []string{string(metadataJSON)},
					}},
				} {
					intent := runningHubHook(t, plugin, []string{"protocols", "openai_video", "decodeRequest"}, map[string]any{
						"model": "local-alias", "upstreamModel": modelName, "body": body,
					})
					assert.Equal(t, "local-alias", intent["model"])
					assert.Equal(t, "image_to_video", intent["action"])
					ctx := map[string]any{"model": "local-alias", "upstreamModel": modelName, "baseUrl": "https://www.runninghub.cn",
						"apiKey": "test-key", "publicTaskId": "task_frames", "requestBody": intent["requestBody"],
					}
					descriptor := runningHubHook(t, plugin, []string{"buildSubmitRequest"}, ctx)
					upstream := descriptor["body"].(map[string]any)
					assert.Equal(t, "2113713138997743617", upstream["workflowId"])
					assert.Equal(t, "plus", upstream["instanceType"])
					var graph map[string]struct {
						Type   string         `json:"class_type"`
						Inputs map[string]any `json:"inputs"`
					}
					require.NoError(t, common.UnmarshalJsonStr(upstream["workflow"].(string), &graph))
					conditioning := "403"
					if modelName == "minimax-h3-standard" {
						conditioning = "1403"
					}
					assert.Equal(t, "MiniMaxH3ImageToVideo", graph[conditioning].Type)
					assert.Equal(t, "a cat walks to the window", graph["409"].Inputs["value"])
					assert.Equal(t, 4.0, graph["17"].Inputs["value"])
					assert.Equal(t, 1344.0, graph[conditioning].Inputs["width"])
					assert.Equal(t, 768.0, graph[conditioning].Inputs["height"])
					for _, tc := range []struct{ field, node, input string }{{"first_frame_image", "274", "first_frame"}, {"last_frame_image", "277", "last_frame"}} {
						if image, ok := frames[tc.field]; ok {
							assert.Equal(t, image, graph[tc.node].Inputs["image"])
							assert.Equal(t, []any{tc.node, 0.0}, graph[conditioning].Inputs[tc.input])
						} else {
							assert.NotContains(t, graph, tc.node)
							assert.NotContains(t, graph[conditioning].Inputs, tc.input)
						}
					}
					for _, node := range graph {
						assert.NotEqual(t, "MiniMaxH3ReferenceToVideo", node.Type)
						assert.NotEqual(t, "LazySwitchKJ", node.Type)
						for _, input := range node.Inputs {
							if link, ok := input.([]any); ok && len(link) == 2 {
								assert.Contains(t, graph, link[0], "selected graph must contain no dangling inputs")
							}
						}
					}
					assert.NotContains(t, graph, "11")
					assert.NotContains(t, graph, "1136")
					assert.NotContains(t, graph, "1127", "reference-only weights must not run for first/last frames")
					if modelName == "minimax-h3-standard" {
						assert.NotContains(t, graph, "233")
						assert.NotContains(t, graph, "402")
						assert.Equal(t, 35.0, graph["1124"].Inputs["steps"])
					} else {
						assert.NotContains(t, graph, "1125")
						assert.Contains(t, graph, "402")
						assert.Equal(t, 8.0, graph["241"].Inputs["steps"])
					}
					assert.Equal(t, map[string]any{"seconds": 107.0 / 24, "resolution": "768P"}, runningHubHook(t, plugin, []string{"extractUsage"}, ctx))
				}
			})
		}
	}

	create := func(body map[string]any) (any, error) {
		return plugin.Engine.CallPath(t.Context(), "native", []string{"createVideo"}, map[string]any{"body": map[string]any{"kind": "json", "value": body}})
	}
	valid := func() map[string]any {
		return map[string]any{"model": "minimax-h3-flash", "duration": 4, "resolution": "768P", "ratio": "9:16", "content": []any{
			map[string]any{"type": "text", "text": "a cat walks to the window"},
			map[string]any{"type": "image_url", "role": "first_frame", "image_url": map[string]any{"url": "api/start.png"}},
			map[string]any{"type": "image_url", "role": "last_frame", "image_url": map[string]any{"url": "openapi/end.png"}},
		}}
	}
	t.Run("official body selects adaptive first last geometry and ignores watermark", func(t *testing.T) {
		var baseline string
		for _, watermark := range []any{false, true, "ignored", nil, map[string]any{"anything": true}} {
			body := valid()
			body["aigc_watermark"] = watermark
			value, err := create(body)
			require.NoError(t, err)
			encoded, err := common.Marshal(value)
			require.NoError(t, err)
			var intent map[string]any
			require.NoError(t, common.Unmarshal(encoded, &intent))
			request := intent["requestBody"].(map[string]any)
			assert.Equal(t, "adaptive", request["ratio"], "MiniMax frame requests inherit input geometry even when a fixed ratio is supplied")
			assert.NotContains(t, request, "aigc_watermark")
			ctx := map[string]any{"model": "minimax-h3-flash", "baseUrl": "https://www.runninghub.cn", "apiKey": "test-key", "publicTaskId": "task_native", "requestBody": request}
			upstream := runningHubHook(t, plugin, []string{"buildSubmitRequest"}, ctx)["body"].(map[string]any)
			if baseline == "" {
				baseline = upstream["workflow"].(string)
			} else {
				assert.JSONEq(t, baseline, upstream["workflow"].(string))
			}
			var graph map[string]struct {
				Type   string         `json:"class_type"`
				Inputs map[string]any `json:"inputs"`
			}
			require.NoError(t, common.UnmarshalJsonStr(upstream["workflow"].(string), &graph))
			assert.Equal(t, "GetImageSize", graph["900"].Type)
			assert.Equal(t, []any{"274", 0.0}, graph["900"].Inputs["image"])
			assert.Equal(t, []any{"901", 1.0}, graph["403"].Inputs["width"])
			assert.Equal(t, []any{"902", 1.0}, graph["403"].Inputs["height"])
			assert.NotContains(t, graph, "322", "adaptive canvas is already aligned for the sampler")
		}
	})
	for _, tc := range []struct {
		name, key string
		value     any
		want      string
	}{
		{"duration zero", "duration", 0, "between 4 and 15"}, {"duration large", "duration", 1e20, "between 4 and 15"},
		{"duration fractional", "duration", 4.5, "between 4 and 15"}, {"duration boolean", "duration", true, "duration must be"},
		{"unsupported resolution", "resolution", "2K", "size must be"}, {"invalid ratio", "ratio", "1000:1", "ratio must be"},
		{"invalid content", "content", []any{true}, "must be an object"}, {"no text", "content", []any{map[string]any{"type": "text", "text": " "}}, "non-empty text"},
		{"callbacks", "callback_url", "https://example.com/hook", "callback_url is not supported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := valid()
			body[tc.key] = tc.value
			_, err := create(body)
			require.ErrorContains(t, err, tc.want)
		})
	}
	t.Run("mixed roles and duplicate frames are rejected", func(t *testing.T) {
		for _, extra := range []map[string]any{
			{"type": "image_url", "role": "first_frame", "image_url": map[string]any{"url": "api/second.png"}},
			{"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "api/reference.png"}},
			{"type": "video_url", "role": "reference_video", "video_url": map[string]any{"url": "api/ref.mp4"}},
			{"type": "audio_url", "role": "reference_audio", "audio_url": map[string]any{"url": "api/ref.wav"}},
		} {
			body := valid()
			body["content"] = append(body["content"].([]any), extra)
			_, err := create(body)
			require.Error(t, err)
		}
	})
	t.Run("official text and reference inputs use the selected model branch", func(t *testing.T) {
		for _, modelName := range []string{"minimax-h3-flash", "minimax-h3-standard"} {
			for _, ratio := range []string{"21:9", "16:9", "4:3", "1:1", "3:4", "9:16", "adaptive"} {
				body := valid()
				body["model"] = modelName
				body["ratio"] = ratio
				body["content"] = []any{map[string]any{"type": "text", "text": "a cat"}}
				if ratio == "adaptive" {
					body["content"] = append(body["content"].([]any),
						map[string]any{"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "api/ref.png"}},
						map[string]any{"type": "video_url", "role": "reference_video", "video_url": map[string]any{"url": "api/ref.mp4"}},
						map[string]any{"type": "audio_url", "role": "reference_audio", "audio_url": map[string]any{"url": "api/ref.wav"}})
				}
				value, err := create(body)
				require.NoError(t, err)
				encoded, err := common.Marshal(value)
				require.NoError(t, err)
				var intent map[string]any
				require.NoError(t, common.Unmarshal(encoded, &intent))
				ctx := map[string]any{"model": modelName, "requestBody": intent["requestBody"], "baseUrl": "https://www.runninghub.cn", "apiKey": "test-key"}
				upstream := runningHubHook(t, plugin, []string{"buildSubmitRequest"}, ctx)["body"].(map[string]any)
				var graph map[string]struct {
					Type   string         `json:"class_type"`
					Inputs map[string]any `json:"inputs"`
				}
				require.NoError(t, common.UnmarshalJsonStr(upstream["workflow"].(string), &graph))
				conditioning := "11"
				if modelName == "minimax-h3-standard" {
					conditioning = "1136"
				}
				assert.Equal(t, "MiniMaxH3ReferenceToVideo", graph[conditioning].Type)
				assert.Equal(t, "a cat", graph["2"].Inputs["value"])
				assert.NotContains(t, graph, "403")
				assert.NotContains(t, graph, "1403")
				assert.NotContains(t, graph, "UPLOAD_")
				if ratio == "adaptive" {
					assert.Equal(t, "api/ref.png", graph["274"].Inputs["image"])
					assert.Equal(t, "api/ref.mp4", graph["301"].Inputs["video"])
					assert.Equal(t, "api/ref.wav", graph["310"].Inputs["audio"])
				} else {
					width := graph[conditioning].Inputs["width"].(float64)
					height := graph[conditioning].Inputs["height"].(float64)
					assert.GreaterOrEqual(t, width, 32.0)
					assert.GreaterOrEqual(t, height, 32.0)
					assert.LessOrEqual(t, width*height, 1664.0*800, "aspect ratio cannot expand beyond the selected resolution budget plus canvas padding")
				}
				assert.Equal(t, map[string]any{"seconds": 107.0 / 24, "resolution": "768P"}, runningHubHook(t, plugin, []string{"extractUsage"}, ctx))
			}
		}
	})
	t.Run("native routes retain the model scope and protocol remains video only", func(t *testing.T) {
		registry := jsplugin.NewRegistry()
		source, err := builtinplugins.Source("runninghub")
		require.NoError(t, err)
		_, err = registry.RegisterFactory(source, jsplugin.Options{})
		require.NoError(t, err)
		binding, found := registry.Generation().LookupDeclaredRoute("POST", "/runninghub/v2/video_generation")
		require.True(t, found)
		assert.ElementsMatch(t, []string{"minimax-h3-flash", "minimax-h3-standard"}, binding.Route.Models)
		_, found = registry.Generation().LookupDeclaredRoute("GET", "/runninghub/v2/query/video_generation/:task_id")
		require.True(t, found)
	})
	t.Run("native task envelope and authoritative terminal state", func(t *testing.T) {
		assert.Equal(t, map[string]any{"task_id": "task_public"}, runningHubHook(t, plugin, []string{"native", "videoCreated"}, map[string]any{}, map[string]any{"task_id": "task_public"}))
		for _, tc := range []struct{ status, want string }{{"QUEUED", "queued"}, {"IN_PROGRESS", "running"}, {"SUCCESS", "succeeded"}, {"FAILURE", "failed"}} {
			view, err := service.BuildTaskPluginView(&model.Task{
				TaskID: "task_public", Status: model.TaskStatus(tc.status), UpdatedAt: 200, FailReason: "cancelled upstream",
				Properties:  model.Properties{OriginModelName: "minimax-h3-standard", UpstreamModelName: "private-model"},
				PrivateData: model.TaskPrivateData{UpstreamTaskID: "private-id", Key: "private-credential"},
				Data:        []byte(`{"taskId":"private-id","status":"RUNNING","results":[{"url":"https://cdn.example/video.mp4","outputType":"mp4"}]}`),
			})
			require.NoError(t, err)
			encoded, err := common.Marshal(view)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "private-model")
			assert.NotContains(t, string(encoded), "private-credential")
			var task map[string]any
			require.NoError(t, common.Unmarshal(encoded, &task))
			output := runningHubHook(t, plugin, []string{"native", "videoStatus"}, map[string]any{}, task)["task"].(map[string]any)
			assert.Equal(t, "task_public", output["id"])
			assert.Equal(t, tc.want, output["status"])
			assert.Equal(t, "minimax-h3-standard", output["model"])
			assert.NotContains(t, output, "taskId")
			if tc.status == "SUCCESS" {
				assert.Equal(t, map[string]any{"url": "https://cdn.example/video.mp4"}, output["content"])
			} else {
				assert.NotContains(t, output, "content")
			}
			if tc.status == "FAILURE" {
				assert.Equal(t, "cancelled upstream", output["error"].(map[string]any)["message"])
			}
		}
	})
}

func TestRunningHubTaskLifecycleAndArtifacts(t *testing.T) {
	plugin := runningHubPlugin(t)
	const upstreamID = "2113683557746036738"
	ctx := map[string]any{
		"model": "minimax-h3-flash", "upstreamModel": "minimax-h3-flash", "requestBody": map[string]any{"prompt": "a cat"},
		"baseUrl": "https://www.runninghub.cn", "apiKey": "test-key", "taskId": upstreamID,
	}
	submitted := runningHubHook(t, plugin, []string{"parseSubmitResponse"}, ctx, map[string]any{"body": map[string]any{
		"code": 0, "data": map[string]any{"taskId": upstreamID, "taskStatus": "QUEUED", "netWssUrl": "wss://private.example/?token=must-not-persist"},
	}})
	assert.Equal(t, upstreamID, submitted["taskId"])
	assert.NotContains(t, submitted["taskData"], "netWssUrl")
	_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", ctx, map[string]any{"body": map[string]any{"code": 416, "msg": "insufficient balance"}})
	require.ErrorContains(t, err, "insufficient balance")
	_, err = plugin.Engine.Call(t.Context(), "parseSubmitResponse", ctx, map[string]any{"body": map[string]any{"code": 0, "data": map[string]any{"taskId": float64(2113683557746036738)}}})
	require.ErrorContains(t, err, "taskId must be")

	query := runningHubHook(t, plugin, []string{"buildQueryRequest"}, ctx)
	assert.Equal(t, "https://www.runninghub.cn/openapi/v2/query", query["url"])
	assert.Equal(t, "POST", query["method"])
	assert.Equal(t, map[string]any{"taskId": upstreamID}, query["body"])
	assert.Equal(t, "Bearer test-key", query["headers"].(map[string]any)["Authorization"])

	for _, tc := range []struct{ status, want string }{
		{"QUEUED", "QUEUED"}, {"RUNNING", "IN_PROGRESS"}, {"FAILED", "FAILURE"}, {"CANCELLED", "FAILURE"}, {"", "UNKNOWN"}, {"new-vendor-state", "UNKNOWN"}, {"toString", "UNKNOWN"}, {"SUCCESS", "UNKNOWN"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			result := runningHubHook(t, plugin, []string{"parseTaskResult"}, ctx, map[string]any{"taskId": upstreamID, "status": tc.status})
			assert.Equal(t, tc.want, result["status"])
		})
	}
	const videoURL = "https://cdn.example/video.mp4?signature=download"
	data := map[string]any{"taskId": upstreamID, "status": "SUCCESS", "results": []any{
		map[string]any{"url": "https://cdn.example/preview.jpg", "outputType": "jpg"},
		map[string]any{"url": videoURL, "outputType": "mp4", "taskCostTime": 3600},
	}}
	result := runningHubHook(t, plugin, []string{"parseTaskResult"}, ctx, data)
	assert.Equal(t, "SUCCESS", result["status"])
	assert.Equal(t, videoURL, result["url"])
	ctx["state"] = submitted["state"]
	assert.Equal(t, map[string]any{"seconds": 124.0 / 24, "resolution": "480P"}, runningHubHook(t, plugin, []string{"extractUsageOnComplete"}, ctx, result, data))
	ctx["state"] = map[string]any{"usage": map[string]any{"seconds": 124.0 / 24, "resolution": "480P", "instance_type": "default"}}
	assert.Equal(t, map[string]any{"seconds": 124.0 / 24, "resolution": "480P"}, runningHubHook(t, plugin, []string{"extractUsageOnComplete"}, ctx, result, data), "old persisted usage still produces facts matching the new schema")

	encoded, err := common.Marshal(data)
	require.NoError(t, err)
	adaptor := taskplugin.New(plugin)
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "must-not-reach-cdn", ChannelBaseUrl: "https://www.runninghub.cn"}})
	task := &model.Task{TaskID: "task_public", Status: model.TaskStatusSuccess, Progress: "100%", Data: encoded,
		Properties: model.Properties{OriginModelName: "minimax-h3-flash"},
	}
	artifacts, err := adaptor.ListArtifacts(task)
	require.NoError(t, err)
	assert.Equal(t, []channel.TaskArtifact{{Key: "video", Type: "video", MimeType: "video/mp4"}}, artifacts)
	content, err := adaptor.BuildContentRequest(t.Context(), task, "video", channel.TaskArtifactClientRequest{Method: http.MethodHead})
	require.NoError(t, err)
	assert.Equal(t, &channel.TaskContentRequest{URL: videoURL, Method: http.MethodHead, Credentialless: true}, content)
	video, err := adaptor.ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	var response map[string]any
	require.NoError(t, common.Unmarshal(video, &response))
	assert.Equal(t, "task_public", response["id"])
	assert.Equal(t, "video", response["object"])
	assert.Equal(t, "completed", response["status"])
	assert.Equal(t, "minimax-h3-flash", response["model"])
	assert.Equal(t, videoURL, response["url"])
	assert.NotContains(t, string(video), upstreamID, "private vendor IDs must not escape the gateway video response")
}

func TestRunningHubOnlyAdvertisesVideoProtocol(t *testing.T) {
	plugin := runningHubPlugin(t)
	require.Len(t, plugin.Meta.Protocols, 1)
	assert.Equal(t, "openai_video", plugin.Meta.Protocols[0].Name)
	assert.NotContains(t, plugin.Meta.UsageSchema, "instance_type")
	for _, example := range plugin.Meta.UsageExamples {
		assert.NotContains(t, example.Label, "frames")
		assert.NotContains(t, example.Facts, "instance_type")
	}
}

func TestRunningHubRetiredOptionsCannotChangeExecution(t *testing.T) {
	plugin := runningHubPlugin(t)
	ctx := map[string]any{
		"model": "minimax-h3-flash", "baseUrl": "https://www.runninghub.cn", "apiKey": "test-key", "publicTaskId": "task_retry",
		"requestBody": map[string]any{"prompt": "a cat", "size": "768P", "seconds": 15},
	}
	baseline := runningHubHook(t, plugin, []string{"buildSubmitRequest"}, ctx)
	ctx["requestBody"] = map[string]any{
		"prompt": "a cat", "size": "768P", "seconds": 15,
		"seed": 12345, "instance_type": "plus", "use_personal_queue": true,
		"instanceType": "ultra", "usePersonalQueue": true,
		"metadata": map[string]any{
			"seed": 12345, "instance_type": "plus", "use_personal_queue": true,
			"instanceType": "ultra", "usePersonalQueue": true,
		},
	}
	withRetiredOptions := runningHubHook(t, plugin, []string{"buildSubmitRequest"}, ctx)
	baselineBody := baseline["body"].(map[string]any)
	retiredBody := withRetiredOptions["body"].(map[string]any)
	assert.JSONEq(t, baselineBody["workflow"].(string), retiredBody["workflow"].(string), "retired client options cannot override the workflow or internal seed")
	assert.Equal(t, baselineBody["nodeInfoList"], retiredBody["nodeInfoList"], "retries preserve the internally generated seed")
	assert.Equal(t, "plus", retiredBody["instanceType"], "a client cannot replace the instance selected by output resolution")
	assert.NotContains(t, retiredBody, "usePersonalQueue")
	ctx["publicTaskId"] = "task_new_generation"
	nextTask := runningHubHook(t, plugin, []string{"buildSubmitRequest"}, ctx)
	assert.NotEqual(t, baselineBody["nodeInfoList"], nextTask["body"].(map[string]any)["nodeInfoList"], "a new public task gets a fresh seed while retries remain stable")
}

func TestRunningHubChannelBalance(t *testing.T) {
	previousRate := operation_setting.USDExchangeRate
	operation_setting.USDExchangeRate = 7
	t.Cleanup(func() { operation_setting.USDExchangeRate = previousRate })
	plugin := runningHubPlugin(t)
	for _, tc := range []struct {
		name, body, wantError string
		status                int
		want                  float64
	}{
		{name: "CNY cash converted to USD", body: `{"code":0,"data":{"remainMoney":"51.4230","currency":"CNY","remainCoins":"51452"}}`, want: 7.346142857142857},
		{name: "USD cash unchanged", body: `{"code":0,"data":{"remainMoney":"12.50","currency":"USD"}}`, want: 12.5},
		{name: "zero cash preserved", body: `{"code":0,"data":{"remainMoney":0,"currency":"CNY","remainCoins":"51452"}}`},
		{name: "credits are not cash", body: `{"code":0,"data":{"remainCoins":"51452","currency":"CNY"}}`, wantError: "valid cash balance"},
		{name: "null cash rejected", body: `{"code":0,"data":{"remainMoney":null,"currency":"CNY"}}`, wantError: "valid cash balance"},
		{name: "negative cash rejected", body: `{"code":0,"data":{"remainMoney":"-1","currency":"CNY"}}`, wantError: "valid cash balance"},
		{name: "boolean cash rejected", body: `{"code":0,"data":{"remainMoney":false,"currency":"CNY"}}`, wantError: "valid cash balance"},
		{name: "empty cash rejected", body: `{"code":0,"data":{"remainMoney":"","currency":"CNY"}}`, wantError: "valid cash balance"},
		{name: "nonfinite cash rejected", body: `{"code":0,"data":{"remainMoney":"Infinity","currency":"CNY"}}`, wantError: "valid cash balance"},
		{name: "unknown currency rejected", body: `{"code":0,"data":{"remainMoney":"10","currency":"RH"}}`, wantError: "currency is unsupported"},
		{name: "vendor failure", body: `{"code":1002,"msg":"invalid API key"}`, wantError: "account query failed"},
		{name: "HTTP failure cannot become zero cash", status: http.StatusUnauthorized, body: `{"code":0,"data":{"remainMoney":0,"currency":"USD"}}`, wantError: "HTTP 401"},
		{name: "malformed JSON", body: `not-json`, wantError: "invalid balance JSON"},
		{name: "oversized body", body: strings.Repeat(" ", (256<<10)+1), wantError: "balance response exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/uc/openapi/accountStatus", r.URL.Path)
				assert.Empty(t, r.URL.RawQuery)
				assert.Equal(t, "Bearer test-balance-key", r.Header.Get("Authorization"))
				var body map[string]string
				require.NoError(t, common.DecodeJson(r.Body, &body))
				assert.Equal(t, map[string]string{"apikey": "test-balance-key"}, body)
				w.Header().Set("Content-Type", "application/json")
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			adaptor := taskplugin.New(plugin)
			adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: constant.ChannelTypeTaskPlugin, ApiKey: "test-balance-key", ChannelBaseUrl: server.URL,
			}})
			balance, err := adaptor.QueryBalance(t.Context())
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			assert.InDelta(t, tc.want, balance, 1e-10)
		})
	}
}

func TestRunningHubBalanceHooksMustBePairedFunctions(t *testing.T) {
	source, err := builtinplugins.Source("runninghub")
	require.NoError(t, err)
	for _, broken := range []string{
		strings.Replace(source, "export function parseBalanceResponse", "function parseBalanceResponse", 1),
		strings.Replace(source, "export function buildBalanceRequest", "function buildBalanceRequest", 1),
		strings.Replace(source, "export function parseBalanceResponse", "function localBalanceResponse", 1) + "\nexport const parseBalanceResponse = 1;",
	} {
		_, err := jsplugin.CompilePlugin(broken, jsplugin.Options{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Balance")
	}
}
