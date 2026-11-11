package controller

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// CancelLocalH3VideoTask retains the original charge for a confirmed cancellation.
func CancelLocalH3VideoTask(c *gin.Context) {
	taskID := strings.TrimSpace(c.Param("video_id"))
	task, exists, err := model.GetByTaskId(c.GetInt("id"), taskID)
	if err != nil {
		respondTaskError(c, service.TaskErrorWrapper(err, "get_task_failed", http.StatusInternalServerError))
		return
	}
	if !exists || task == nil {
		respondTaskError(c, service.TaskErrorWrapperLocal(fmt.Errorf("task not found"), "task_not_found", http.StatusNotFound))
		return
	}
	channel, err := model.GetChannelById(task.ChannelId, true)
	if err != nil {
		respondTaskError(c, service.TaskErrorWrapper(err, "get_channel_failed", http.StatusInternalServerError))
		return
	}
	if channel.Type != constant.ChannelTypeTaskPlugin || string(task.Platform) != "local-h3" {
		respondTaskError(c, service.TaskErrorWrapperLocal(fmt.Errorf("task cancellation is not supported for this channel"), "task_cancellation_not_supported", http.StatusNotImplemented))
		return
	}
	if task.Status == model.TaskStatusFailure && task.FailReason == "cancelled by user" {
		c.JSON(http.StatusOK, gin.H{"id": taskID, "status": "cancelled"})
		return
	}
	if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
		respondTaskError(c, service.TaskErrorWrapperLocal(fmt.Errorf("task already finished"), "task_already_finished", http.StatusConflict))
		return
	}
	if task.Status != model.TaskStatusCancelPending {
		previous := task.Status
		task.Status = model.TaskStatusCancelPending
		won, err := task.UpdateWithStatus(previous)
		if err != nil {
			respondTaskError(c, service.TaskErrorWrapper(err, "update_task_failed", http.StatusInternalServerError))
			return
		}
		if !won {
			respondTaskError(c, service.TaskErrorWrapperLocal(fmt.Errorf("task state changed"), "task_state_changed", http.StatusConflict))
			return
		}
	}
	key := task.PrivateData.Key
	if key == "" {
		key = channel.Key
	}
	client, err := service.GetHttpClientWithProxySettings(channel.GetSetting().Proxy, channel.GetSetting())
	if err != nil {
		respondTaskError(c, service.TaskErrorWrapper(err, "cancel_task_failed", http.StatusBadGateway))
		return
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	target := strings.TrimRight(channel.GetBaseURL(), "/") + "/v2/video_generation/" + url.PathEscape(task.GetUpstreamTaskID()) + "/cancel"
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, target, nil)
	if err != nil {
		respondTaskError(c, service.TaskErrorWrapper(err, "cancel_task_failed", http.StatusBadGateway))
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := clientCopy.Do(req)
	if err != nil {
		respondTaskError(c, service.TaskErrorWrapper(err, "cancel_task_unconfirmed", http.StatusBadGateway))
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if err != nil || resp.StatusCode != http.StatusOK || common.Unmarshal(body, &result) != nil || result.TaskID != task.GetUpstreamTaskID() || result.Status != "cancelled" {
		respondTaskError(c, service.TaskErrorWrapperLocal(fmt.Errorf("upstream did not confirm cancellation; task status will be reconciled"), "cancel_task_unconfirmed", http.StatusBadGateway))
		return
	}
	task.Status = model.TaskStatusFailure
	task.FailReason = "cancelled by user"
	task.Progress = "100%"
	task.FinishTime = time.Now().Unix()
	won, err := task.UpdateWithStatus(model.TaskStatusCancelPending)
	if err != nil {
		respondTaskError(c, service.TaskErrorWrapper(err, "update_task_failed", http.StatusInternalServerError))
		return
	}
	if !won {
		current, found, loadErr := model.GetByTaskId(c.GetInt("id"), taskID)
		if loadErr != nil || !found || current.Status != model.TaskStatusFailure || current.FailReason != "cancelled by user" {
			respondTaskError(c, service.TaskErrorWrapperLocal(fmt.Errorf("task state changed"), "task_state_changed", http.StatusConflict))
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"id": taskID, "status": "cancelled"})
}
