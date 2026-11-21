package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type recordingFixture struct {
	t  *testing.T
	db *gorm.DB
}

func (fixture recordingFixture) capture(owner int, requestID, format, path, contentType string, request []byte, responseType, response string, task *model.Task) *model.RequestRecord {
	t := fixture.t
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(request))
	c.Request.Header.Set("Content-Type", contentType)
	c.Request.Header.Set("X-Conversation-Id", "shared-conversation-id")
	c.Set("id", owner)
	c.Set("token_id", 7)
	c.Set(common.RequestIdKey, requestID)
	finish := service.BeginRequestRecord(c, format)
	c.Set(string(constant.ContextKeyOriginalModel), "test-model")
	upstream, err := http.NewRequest(http.MethodPost, "https://upstream.invalid/v1/generate", strings.NewReader(`{"model":"upstream-model","messages":[{"role":"user","content":"actual upstream prompt"}]}`))
	require.NoError(t, err)
	upstream.Header.Set("Content-Type", "application/json")
	observe := service.CaptureRequestRecordUpstream(c, upstream)
	upstreamType, upstreamBody := "application/json", `{"choices":[{"message":{"role":"assistant","content":"upstream answer"}}]}`
	if responseType == "text/event-stream" {
		upstreamType, upstreamBody = responseType, response
	}
	upstreamResponse := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{upstreamType}}, Body: io.NopCloser(strings.NewReader(upstreamBody))}
	observe(upstreamResponse)
	_, err = io.ReadAll(upstreamResponse.Body)
	require.NoError(t, err)
	require.NoError(t, upstreamResponse.Body.Close())
	if task != nil {
		service.AttachRequestRecordTask(c, task)
	}
	c.Header("Content-Type", responseType)
	_, err = c.Writer.Write([]byte(response))
	require.NoError(t, err)
	finish()
	require.Equal(t, response, w.Body.String(), "recording must not change the client response")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, service.FlushRequestRecords(ctx))
	record, err := model.GetRequestRecord(owner, requestID)
	require.NoError(t, err)
	return record
}

func (fixture recordingFixture) call(handler gin.HandlerFunc, owner, role int, target string, params gin.Params) *httptest.ResponseRecorder {
	fixture.t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	c.Params = params
	c.Set("id", owner)
	c.Set("role", role)
	handler(c)
	c.Writer.WriteHeaderNow()
	return w
}

func TestRequestRecording(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, engine := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var dialector gorm.Dialector
			switch engine {
			case "sqlite":
				dialector = sqlite.Open(filepath.Join(t.TempDir(), "records.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				dialector = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				dialector = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			previousDB, previousLogDB := model.DB, model.LOG_DB
			previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
			previousSettings := *operation_setting.GetRequestRecordSetting()
			model.DB, model.LOG_DB = db, db
			databaseType := common.DatabaseTypeSQLite
			if engine == "mysql" {
				databaseType = common.DatabaseTypeMySQL
			}
			if engine == "postgres" {
				databaseType = common.DatabaseTypePostgreSQL
			}
			common.SetDatabaseTypes(databaseType, databaseType)
			t.Setenv("REQUEST_RECORD_DIR", t.TempDir())
			require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"request_record_setting.enabled": "true", "request_record_setting.retention_days": "7", "request_record_setting.max_body_mb": "1"}))
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				require.NoError(t, service.FlushRequestRecords(ctx))
				require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"request_record_setting.enabled": fmt.Sprint(previousSettings.Enabled), "request_record_setting.retention_days": fmt.Sprint(previousSettings.RetentionDays), "request_record_setting.max_body_mb": fmt.Sprint(previousSettings.MaxBodyMB)}))
				model.DB, model.LOG_DB = previousDB, previousLogDB
				common.SetDatabaseTypes(previousMainType, previousLogType)
				require.NoError(t, sqlDB.Close())
			})
			for _, table := range []any{&model.RequestRecordBlobLink{}, &model.RequestRecordBlob{}, &model.RequestRecord{}, &model.Task{}, &model.Log{}} {
				require.NoError(t, db.Migrator().DropTable(table))
			}
			versionQuery := "SELECT version()"
			if engine == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database version: %s", version)
			// The pre-feature schema contains tasks/logs and their existing data.
			// New tables are additive; repeated startup must preserve those rows.
			require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Log{}))
			legacy := model.Task{TaskID: "legacy-task", UserId: 1, Status: model.TaskStatusSuccess, Properties: model.Properties{Input: "preserved legacy prompt"}}
			require.NoError(t, db.Create(&legacy).Error)
			for range 2 {
				require.NoError(t, db.AutoMigrate(&model.RequestRecord{}, &model.RequestRecordBlob{}, &model.RequestRecordBlobLink{}))
			}
			var preserved model.Task
			require.NoError(t, db.First(&preserved, legacy.ID).Error)
			assert.Equal(t, legacy.Properties, preserved.Properties)
			assert.True(t, db.Migrator().HasIndex(&model.RequestRecord{}, "idx_record_owner_request"))
			fixture := recordingFixture{t: t, db: db}
			body := []byte(`{"model":"test-model","temperature":0,"stream":false,"seed":9223372036854775807,"api_key":"credential-canary","messages":[{"role":"system","content":"shared system instructions"},{"role":"user","content":"雨夜 100%_scene"}]}`)
			record := fixture.capture(1, "request-one", "openai", "/v1/chat/completions", "application/json", body, "application/json", `{"choices":[{"message":{"role":"assistant","content":"client answer"}}]}`, nil)
			detail, err := service.LoadRequestRecordDetail(record)
			require.NoError(t, err)
			require.Len(t, detail.Snapshots, 4)
			assert.Equal(t, "shared-conversation-id", record.ConversationID)
			assert.Equal(t, "test-model", record.ModelName)
			encoded, err := common.Marshal(detail)
			require.NoError(t, err)
			assert.Contains(t, string(encoded), `"temperature":0`)
			assert.Contains(t, string(encoded), `"stream":false`)
			assert.Contains(t, string(encoded), `"seed":9223372036854775807`)
			assert.Contains(t, string(encoded), "actual upstream prompt")
			assert.Contains(t, string(encoded), "upstream answer")
			assert.Contains(t, string(encoded), "client answer")
			assert.NotContains(t, string(encoded), "credential-canary")
			blobs, err := model.GetRequestRecordBlobs(record.ID)
			require.NoError(t, err)
			second := fixture.capture(1, "request-two", "openai", "/v1/chat/completions", "application/json", body, "application/json", `{"choices":[{"message":{"role":"assistant","content":"client answer"}}]}`, nil)
			secondBlobs, err := model.GetRequestRecordBlobs(second.ID)
			require.NoError(t, err)
			var firstIDs, secondIDs []string
			for _, blob := range blobs {
				firstIDs = append(firstIDs, blob.ID)
			}
			for _, blob := range secondBlobs {
				secondIDs = append(secondIDs, blob.ID)
			}
			assert.ElementsMatch(t, firstIDs, secondIDs, "identical history and snapshots must reuse file ids")
			foreign := fixture.capture(2, "request-two", "openai", "/v1/chat/completions", "application/json", body, "application/json", `{}`, nil)
			foreignBlobs, err := model.GetRequestRecordBlobs(foreign.ID)
			require.NoError(t, err)
			for _, blob := range foreignBlobs {
				for _, own := range blobs {
					assert.NotEqual(t, own.ID, blob.ID)
				}
			}
			_, total, err := model.ListRequestRecords(model.RequestRecordFilter{UserID: 1, Keyword: "100%_scene"}, 0, 20)
			require.NoError(t, err)
			assert.EqualValues(t, 2, total, "literal wildcard characters must not widen the search")
			_, err = model.GetRequestRecordByID(record.ID, 2, false)
			assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
			denied := fixture.call(GetRequestRecordDetail, 2, common.RoleCommonUser, "/", gin.Params{{Key: "id", Value: fmt.Sprint(record.ID)}})
			assert.Equal(t, http.StatusNotFound, denied.Code)
			admin := fixture.call(GetRequestRecordDetail, 2, common.RoleAdminUser, "/", gin.Params{{Key: "id", Value: fmt.Sprint(record.ID)}})
			assert.Equal(t, http.StatusOK, admin.Code)
			assert.Contains(t, admin.Body.String(), "client answer")
			ownerSpoof := fixture.call(LookupRequestRecord, 2, common.RoleCommonUser, "/?request_id=request-one&user_id=1", nil)
			assert.Equal(t, http.StatusNotFound, ownerSpoof.Code)
			related := fixture.call(GetRelatedRequestRecords, 1, common.RoleAdminUser, "/?scope=all&user_id=1&conversation_id=shared-conversation-id", nil)
			assert.NotContains(t, related.Body.String(), fmt.Sprintf(`"id":%d,"user_id":2`, foreign.ID))

			t.Run("streaming_and_truncation", func(t *testing.T) {
				fixture.t = t
				stream := "data: {\"choices\":[{\"delta\":{\"content\":\"你好\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"世界\"}}]}\n\ndata: [DONE]\n\n"
				row := fixture.capture(1, "stream-record", "openai", "/v1/chat/completions", "application/json", body, "text/event-stream", stream, nil)
				detail, err := service.LoadRequestRecordDetail(row)
				require.NoError(t, err)
				merged, ok := detail.Snapshots[3].Body.(map[string]any)
				require.True(t, ok, "streamed snapshots must be stored as one merged response")
				choiceList, ok := merged["choices"].([]any)
				require.True(t, ok)
				require.Len(t, choiceList, 1)
				message, ok := choiceList[0].(map[string]any)["message"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "你好世界", message["content"], "deltas must be assembled into the final reply")
				_, total, err := model.ListRequestRecords(model.RequestRecordFilter{UserID: 1, Keyword: "你好世界"}, 0, 20)
				require.NoError(t, err)
				assert.EqualValues(t, 1, total, "search must match the merged reply")
				longStream := strings.Repeat("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n", 5000) + "data: [DONE]\n\n"
				longRow := fixture.capture(1, "long-stream-record", "openai", "/v1/chat/completions", "application/json", body, "text/event-stream", longStream, nil)
				longDetail, err := service.LoadRequestRecordDetail(longRow)
				require.NoError(t, err)
				longMerged, ok := longDetail.Snapshots[3].Body.(map[string]any)
				require.True(t, ok)
				longChoices, ok := longMerged["choices"].([]any)
				require.True(t, ok)
				require.Len(t, longChoices, 1)
				longMessage, ok := longChoices[0].(map[string]any)["message"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, strings.Repeat("a", 5000), longMessage["content"], "a long stream must merge into one reply instead of one file per chunk")
				toolStream := strings.Repeat("data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"a\"}}]}\n\n", 251000) +
					"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"submit_assets\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
				require.Greater(t, len(toolStream), 16<<20) // The configured capture limit applies to stored data, not SSE framing.
				toolRow := fixture.capture(1, "long-tool-stream", "openai", "/v1/chat/completions", "application/json", body, "text/event-stream", toolStream, nil)
				toolDetail, err := service.LoadRequestRecordDetail(toolRow)
				require.NoError(t, err)
				assert.Equal(t, "available", toolRow.BodyState)
				for _, index := range []int{2, 3} {
					encoded, err := common.Marshal(toolDetail.Snapshots[index].Body)
					require.NoError(t, err)
					assert.Contains(t, string(encoded), strings.Repeat("a", 251000))
					assert.Contains(t, string(encoded), `"name":"submit_assets"`)
					assert.Contains(t, string(encoded), `"arguments":"{}"`)
					assert.Contains(t, string(encoded), `"finish_reason":"tool_calls"`)
				}
				row = fixture.capture(1, "truncated-record", "openai", "/v1/chat/completions", "application/json", body, "application/json", strings.Repeat("x", (1<<20)+1), nil)
				assert.Equal(t, "truncated", row.BodyState)
				detail, err = service.LoadRequestRecordDetail(row)
				require.NoError(t, err)
				assert.Equal(t, "truncated", detail.Snapshots[3].State)
				assert.Nil(t, detail.Snapshots[3].Body)
			})
			t.Run("media_and_task_link", func(t *testing.T) {
				fixture.t = t
				media := []byte("RIFF-recorded-image-content")
				encodedMedia := base64.StdEncoding.EncodeToString(media)
				task := model.Task{TaskID: "task-recording", UserId: 1, Status: model.TaskStatusSuccess, SubmitTime: time.Now().Unix(), FinishTime: time.Now().Unix(), Properties: model.Properties{OriginModelName: "image-model"}, PrivateData: model.TaskPrivateData{Execution: &model.TaskExecutionSnapshot{RequestID: "image-record"}}}
				require.NoError(t, db.Create(&task).Error)
				row := fixture.capture(1, "image-record", "openai_image", "/v1/images/generations", "application/json", []byte(`{"prompt":"rainy image","n":1}`), "application/json", `{"data":[{"b64_json":"`+encodedMedia+`"}]}`, &task)
				detail, err := service.LoadRequestRecordDetail(row)
				require.NoError(t, err)
				require.Len(t, detail.Media, 1)
				assert.NotContains(t, row.Manifest, encodedMedia)
				var storedBlobs []model.RequestRecordBlob
				require.NoError(t, db.Find(&storedBlobs).Error)
				for _, blob := range storedBlobs {
					assert.NotContains(t, blob.SearchText, encodedMedia)
				}
				id := detail.Media[0].BlobID
				file, err := service.RequestRecordBlobPath(id)
				require.NoError(t, err)
				data, err := os.ReadFile(file)
				require.NoError(t, err)
				assert.Equal(t, media, data)
				params := gin.Params{{Key: "id", Value: fmt.Sprint(row.ID)}, {Key: "blob", Value: id}}
				assert.Equal(t, http.StatusNotFound, fixture.call(GetRequestRecordMedia, 2, common.RoleCommonUser, "/", params).Code)
				assert.Equal(t, media, fixture.call(GetRequestRecordMedia, 1, common.RoleCommonUser, "/", params).Body.Bytes())
				rangeWriter := httptest.NewRecorder()
				rangeContext, _ := gin.CreateTestContext(rangeWriter)
				rangeContext.Request = httptest.NewRequest(http.MethodGet, "/", nil)
				rangeContext.Request.Header.Set("Range", "bytes=0-3")
				rangeContext.Params = params
				rangeContext.Set("id", 1)
				GetRequestRecordMedia(rangeContext)
				assert.Equal(t, http.StatusPartialContent, rangeWriter.Code)
				assert.Equal(t, media[:4], rangeWriter.Body.Bytes())
				assert.Contains(t, fixture.call(GetTaskRequestRecords, 1, common.RoleCommonUser, "/", gin.Params{{Key: "task_id", Value: task.TaskID}}).Body.String(), "image-record")
				assert.Equal(t, http.StatusNotFound, fixture.call(GetTaskRequestRecords, 2, common.RoleCommonUser, "/", gin.Params{{Key: "task_id", Value: task.TaskID}}).Code)
				service.RecordTaskCompletion(&task)
				service.RecordTaskCompletion(&task)
				require.NoError(t, service.FlushRequestRecords(context.Background()))
				_, total, err := model.ListRequestRecords(model.RequestRecordFilter{UserID: 1, TaskID: task.TaskID}, 0, 20)
				require.NoError(t, err)
				assert.EqualValues(t, 2, total, "terminal task notifications are idempotent")
				pollResponse := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}}
				service.RecordTaskPoll(&task, pollResponse, []byte(`{"status":"SUCCESS","poll":"poll-search-canary"}`), time.Now())
				require.NoError(t, service.FlushRequestRecords(context.Background()))
				linked := fixture.call(GetTaskRequestRecords, 1, common.RoleCommonUser, "/", gin.Params{{Key: "task_id", Value: task.TaskID}})
				assert.Contains(t, linked.Body.String(), `"poll_count":1`)
				assert.NotContains(t, linked.Body.String(), `"operation":"poll"`)
				withPolls := fixture.call(GetTaskRequestRecords, 1, common.RoleCommonUser, "/?include_polls=true", gin.Params{{Key: "task_id", Value: task.TaskID}})
				assert.Contains(t, withPolls.Body.String(), `"operation":"poll"`)
				ids, err := model.MatchingRequestRecordIDs(model.RequestRecordFilter{UserID: 1, Keyword: "poll-search-canary"})
				require.NoError(t, err)
				assert.Equal(t, []string{"image-record"}, ids)
				filter := model.SyncTaskQueryParams{RecordFilter: &model.RequestRecordFilter{UserID: 1, Keyword: "rainy image"}}
				assert.EqualValues(t, 1, model.TaskCountAllUserTask(1, filter))
				assert.Len(t, model.TaskGetAllUserTask(1, 0, 20, filter), 1)
			})
			t.Run("separate_log_database", func(t *testing.T) {
				var logDialector gorm.Dialector
				switch engine {
				case "mysql":
					logDialector = mysql.Open(strings.Replace(os.Getenv("TEST_MYSQL_DSN"), "/record_test?", "/record_logs?", 1))
				case "postgres":
					logDialector = postgres.New(postgres.Config{DSN: strings.Replace(os.Getenv("TEST_POSTGRES_DSN"), "dbname=record_test", "dbname=record_logs", 1), PreferSimpleProtocol: true})
				default:
					logDialector = sqlite.Open(filepath.Join(t.TempDir(), "logs.db"))
				}
				logDB, err := gorm.Open(logDialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
				require.NoError(t, err)
				connection, err := logDB.DB()
				require.NoError(t, err)
				defer connection.Close()
				model.LOG_DB = logDB
				defer func() { model.LOG_DB = db }()
				require.NoError(t, logDB.Migrator().DropTable(&model.Log{}))
				require.NoError(t, logDB.AutoMigrate(&model.Log{}))
				require.NoError(t, logDB.Create(&model.Log{UserId: 1, RequestId: "request-two", CreatedAt: time.Now().Unix(), Type: model.LogTypeConsume, Quota: 123, PromptTokens: 12, CompletionTokens: 3}).Error)
				require.NoError(t, logDB.Create(&model.Log{UserId: 2, RequestId: "request-two", CreatedAt: time.Now().Unix(), Type: model.LogTypeConsume, Quota: 456}).Error)
				filter := &model.RequestRecordFilter{UserID: 1, Keyword: "100%_scene"}
				logs, total, err := model.GetUserLogs(1, model.LogFilter{RecordFilter: filter}, 0, 20)
				require.NoError(t, err)
				assert.EqualValues(t, 1, total)
				require.Len(t, logs, 1)
				assert.Equal(t, 123, logs[0].Quota)
				stat, err := model.SumUsedQuota(model.LogTypeConsume, 0, time.Now().Unix()+1, "", "", "", 0, "", filter)
				require.NoError(t, err)
				assert.Equal(t, 123, stat.Quota)
			})
			t.Run("multipart_and_long_context", func(t *testing.T) {
				fixture.t = t
				var body bytes.Buffer
				writer := multipart.NewWriter(&body)
				require.NoError(t, writer.WriteField("prompt", "edit reference"))
				part, err := writer.CreateFormFile("image", "reference.png")
				require.NoError(t, err)
				_, err = part.Write([]byte("reference-file"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				row := fixture.capture(1, "form-record", "openai_image", "/v1/images/edits", writer.FormDataContentType(), body.Bytes(), "application/json", `{}`, nil)
				detail, err := service.LoadRequestRecordDetail(row)
				require.NoError(t, err)
				require.Len(t, detail.Media, 1)
				assert.Equal(t, "edit reference", detail.Snapshots[0].Body.(map[string]any)["prompt"])
				longText := strings.Repeat("长上下文", 9000) + "TAIL_SEARCH_CANARY"
				payload, err := common.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": longText}}, "_record_ref": "client-literal"})
				require.NoError(t, err)
				row = fixture.capture(1, "long-record", "openai", "/v1/chat/completions", "application/json", payload, "application/json", `{}`, nil)
				detail, err = service.LoadRequestRecordDetail(row)
				require.NoError(t, err)
				data, err := common.Marshal(detail.Snapshots[0].Body)
				require.NoError(t, err)
				assert.Contains(t, string(data), longText)
				assert.Contains(t, string(data), "client-literal")
				_, total, err := model.ListRequestRecords(model.RequestRecordFilter{UserID: 1, Keyword: "TAIL_SEARCH_CANARY"}, 0, 20)
				require.NoError(t, err)
				assert.EqualValues(t, 1, total)
			})
			t.Run("expiry_preserves_shared_files", func(t *testing.T) {
				fixture.t = t
				require.NoError(t, db.Model(&model.RequestRecord{}).Where("id = ?", record.ID).Update("expires_at", time.Now().Unix()-1).Error)
				require.NoError(t, model.ExpireRequestRecords(time.Now().Unix(), 7))
				require.NoError(t, model.CleanupRequestRecordBlobs(common.NodeName, func(id string) error {
					path, err := service.RequestRecordBlobPath(id)
					if err != nil {
						return err
					}
					return os.Remove(path)
				}))
				refreshed, err := model.GetRequestRecord(1, "request-two")
				require.NoError(t, err)
				detail, err := service.LoadRequestRecordDetail(refreshed)
				require.NoError(t, err)
				assert.Equal(t, "available", detail.Snapshots[0].State)
				old, err := model.GetRequestRecord(1, "request-one")
				require.NoError(t, err)
				detail, err = service.LoadRequestRecordDetail(old)
				require.NoError(t, err)
				assert.Equal(t, "expired", detail.Record.BodyState)
				assert.Empty(t, detail.Snapshots)
				orphan := model.RequestRecordBlob{ID: strings.Repeat("f", 64), NodeName: "previous-container"}
				require.NoError(t, db.Create(&orphan).Error)
				require.NoError(t, model.AdoptOrphanRequestRecordBlob(orphan.ID, common.NodeName))
				require.NoError(t, model.CleanupRequestRecordBlobs(common.NodeName, func(id string) error {
					assert.Equal(t, orphan.ID, id)
					return nil
				}))
				var remaining int64
				require.NoError(t, db.Model(&model.RequestRecordBlob{}).Where("id = ?", orphan.ID).Count(&remaining).Error)
				assert.Zero(t, remaining, "a renamed container must still reclaim unreferenced local files")
			})
		})
	}
}
