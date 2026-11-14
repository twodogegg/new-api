package controller

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func requestRecordFilterFromQuery(c *gin.Context, admin bool) *model.RequestRecordFilter {
	keyword := strings.TrimSpace(c.Query("content_keyword"))
	kind, state := c.Query("request_kind"), c.Query("body_state")
	conversation, run := c.Query("conversation_id"), c.Query("run_id")
	if keyword == "" && kind == "" && state == "" && conversation == "" && run == "" {
		return nil
	}
	return &model.RequestRecordFilter{
		UserID: c.GetInt("id"), Admin: admin,
		Keyword: keyword, Kind: kind, BodyState: state, ConversationID: conversation, RunID: run,
	}
}

func LookupRequestRecord(c *gin.Context) {
	owner := c.GetInt("id")
	if requested := c.Query("user_id"); requested != "" {
		id, err := strconv.Atoi(requested)
		if err != nil || id <= 0 || id != owner && c.GetInt("role") < common.RoleAdminUser {
			common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Request record not found"))
			return
		}
		owner = id
	}
	record, err := model.GetRequestRecord(owner, c.Query("request_id"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiSuccess(c, gin.H{"record": nil, "snapshots": []any{}, "media": []any{}, "state": "not_captured"})
		return
	}
	if err != nil {
		common.ApiErrorT(c, "Failed to load request record")
		return
	}
	detail, err := service.LoadRequestRecordDetail(record)
	if err != nil {
		common.ApiErrorT(c, "Failed to load request record")
		return
	}
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, detail)
}

func GetRequestRecordDetail(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	record, err := model.GetRequestRecordByID(id, c.GetInt("id"), c.GetInt("role") >= common.RoleAdminUser)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Request record not found"))
		return
	}
	detail, err := service.LoadRequestRecordDetail(record)
	if err != nil {
		common.ApiErrorT(c, "Failed to load request record")
		return
	}
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, detail)
}

func GetRelatedRequestRecords(c *gin.Context) {
	filter := requestRecordFilterFromQuery(c, c.GetInt("role") >= common.RoleAdminUser && c.Query("scope") == "all")
	if filter == nil {
		filter = &model.RequestRecordFilter{UserID: c.GetInt("id"), Admin: c.GetInt("role") >= common.RoleAdminUser && c.Query("scope") == "all"}
	}
	filter.TaskID, filter.RequestID = c.Query("task_id"), c.Query("request_id")
	if owner, err := strconv.Atoi(c.Query("user_id")); err == nil && owner > 0 {
		if owner != c.GetInt("id") && !filter.Admin {
			common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Request record not found"))
			return
		}
		filter.OwnerID = owner
	}
	filter.StartTimestamp, _ = strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	filter.EndTimestamp, _ = strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	page := common.GetPageQuery(c)
	records, total, err := model.ListRequestRecords(*filter, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiErrorT(c, "Failed to load request record")
		return
	}
	page.SetItems(records)
	page.SetTotal(int(total))
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, page)
}

func GetTaskRequestRecords(c *gin.Context) {
	task, exists, err := getTaskForArtifactRequest(c, c.Param("task_id"))
	if err != nil || !exists {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Task or artifact not found"))
		return
	}
	page := common.GetPageQuery(c)
	filter := model.RequestRecordFilter{UserID: task.UserId, TaskID: task.TaskID}
	_, allCount, err := model.ListRequestRecords(filter, 0, 1)
	if err != nil {
		common.ApiErrorT(c, "Failed to load request record")
		return
	}
	filter.ExcludePolls = true
	_, summaryCount, err := model.ListRequestRecords(filter, 0, 1)
	if err != nil {
		common.ApiErrorT(c, "Failed to load request record")
		return
	}
	filter.ExcludePolls = c.Query("include_polls") != "true"
	records, total, err := model.ListRequestRecords(filter, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiErrorT(c, "Failed to load request record")
		return
	}
	page.SetItems(records)
	page.SetTotal(int(total))
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, gin.H{"items": records, "total": total, "page_size": page.GetPageSize(), "poll_count": allCount - summaryCount})
}

func GetRequestRecordMedia(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	record, err := model.GetRequestRecordByID(id, c.GetInt("id"), c.GetInt("role") >= common.RoleAdminUser)
	if err != nil || model.RequestRecordExpired(record, operation_setting.GetRequestRecordSetting().RetentionDays) {
		c.Status(http.StatusNotFound)
		return
	}
	blobs, err := model.GetRequestRecordBlobs(record.ID)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	for _, blob := range blobs {
		if blob.ID != c.Param("blob") || blob.MimeType == "application/json" {
			continue
		}
		path, err := service.RequestRecordBlobPath(blob.ID)
		if err != nil {
			break
		}
		file, err := os.Open(path)
		if err != nil {
			break
		}
		defer file.Close()
		c.Header("Cache-Control", "private, no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Content-Security-Policy", "sandbox; default-src 'none'")
		c.Header("Content-Type", blob.MimeType)
		if blob.MimeType == "image/svg+xml" || !(strings.HasPrefix(blob.MimeType, "image/") || strings.HasPrefix(blob.MimeType, "video/") || strings.HasPrefix(blob.MimeType, "audio/")) {
			c.Header("Content-Disposition", "attachment; filename=record-file")
		}
		http.ServeContent(c.Writer, c.Request, blob.ID, time.Time{}, file)
		return
	}
	c.Status(http.StatusNotFound)
}

func GetRequestRecordingStatus(c *gin.Context) {
	bytes, err := service.RequestRecordStorageUsage()
	if err != nil {
		common.ApiErrorT(c, "Failed to load recording storage usage")
		return
	}
	common.ApiSuccess(c, gin.H{"settings": operation_setting.GetRequestRecordSetting(), "bytes": bytes, "node_name": common.NodeName})
}
