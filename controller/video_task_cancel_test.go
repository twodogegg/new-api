package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCancelLocalH3VideoTask(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.Channel{}, &model.Task{})
	previousDB, previousType := model.DB, common.MainDatabaseType()
	model.DB = db
	common.SetDatabaseTypes(dialect, common.LogDatabaseType())
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetDatabaseTypes(previousType, common.LogDatabaseType())
	})
	called := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v2/video_generation/upstream/cancel", r.URL.Path)
		assert.Equal(t, "Bearer saved-key", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"task_id":"upstream","status":"cancelled"}`))
	}))
	defer upstream.Close()
	channel := &model.Channel{Type: constant.ChannelTypeTaskPlugin, Key: "channel-key", BaseURL: common.GetPointer(upstream.URL)}
	require.NoError(t, db.Create(channel).Error)
	task := &model.Task{TaskID: "task_public", UserId: 17, ChannelId: channel.Id, Platform: "local-h3", Status: model.TaskStatusQueued, Quota: 1234, PrivateData: model.TaskPrivateData{UpstreamTaskID: "upstream", Key: "saved-key"}}
	require.NoError(t, task.Insert())
	invoke := func(user int) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodDelete, "/v1/videos/task_public", nil)
		ctx.Params = gin.Params{{Key: "video_id", Value: "task_public"}}
		ctx.Set("id", user)
		CancelLocalH3VideoTask(ctx)
		return recorder
	}
	assert.Equal(t, http.StatusNotFound, invoke(18).Code)
	assert.Zero(t, called)
	response := invoke(17)
	assert.Equal(t, http.StatusOK, response.Code)
	assert.JSONEq(t, `{"id":"task_public","status":"cancelled"}`, response.Body.String())
	var reloaded model.Task
	require.NoError(t, db.First(&reloaded, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), reloaded.Status)
	assert.Equal(t, "cancelled by user", reloaded.FailReason)
	assert.Equal(t, 1234, reloaded.Quota)
	assert.Equal(t, http.StatusOK, invoke(17).Code)
	assert.Equal(t, 1, called)
}
