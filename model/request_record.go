package model

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RequestRecord keeps searchable provenance apart from consumption logs. Bodies
// and media are content-addressed files; Manifest contains references only.
type RequestRecord struct {
	ID             int64  `json:"id" gorm:"primaryKey"`
	UserID         int    `json:"user_id" gorm:"uniqueIndex:idx_record_owner_request,priority:1;index:idx_record_owner_time,priority:1"`
	RequestID      string `json:"request_id" gorm:"type:varchar(191);uniqueIndex:idx_record_owner_request,priority:2"`
	TokenID        int    `json:"token_id"`
	ChannelID      int    `json:"channel_id"`
	ModelName      string `json:"model_name" gorm:"type:varchar(191)"`
	Kind           string `json:"kind" gorm:"type:varchar(16);index"`
	Operation      string `json:"operation" gorm:"type:varchar(32)"`
	RequestPath    string `json:"request_path" gorm:"type:text"`
	ConversationID string `json:"conversation_id" gorm:"type:varchar(191);index"`
	RunID          string `json:"run_id" gorm:"type:varchar(191);index"`
	TaskID         string `json:"task_id" gorm:"type:varchar(191);index"`
	ParentID       string `json:"parent_request_id" gorm:"type:varchar(191)"`
	Status         string `json:"status" gorm:"type:varchar(32)"`
	BodyState      string `json:"body_state" gorm:"type:varchar(16)"`
	CreatedAt      int64  `json:"created_at" gorm:"index:idx_record_owner_time,priority:2"`
	ExpiresAt      int64  `json:"expires_at" gorm:"index"`
	DurationMS     int64  `json:"duration_ms"`
	NodeName       string `json:"node_name" gorm:"type:varchar(191)"`
	Manifest       string `json:"-" gorm:"type:text"`
}

// A blob is scoped to an owner and a storage node before it is hashed. SearchText
// contains sanitized textual content, never a second copy of the full context.
type RequestRecordBlob struct {
	ID         string `json:"id" gorm:"type:varchar(64);primaryKey"`
	NodeName   string `json:"-" gorm:"type:varchar(191);index"`
	MimeType   string `json:"mime_type" gorm:"type:varchar(128)"`
	Size       int64  `json:"size"`
	SearchText string `json:"-" gorm:"type:text"`
	CreatedAt  int64  `json:"-" gorm:"index"`
}

type RequestRecordBlobLink struct {
	RecordID int64  `gorm:"primaryKey;autoIncrement:false"`
	BlobID   string `gorm:"type:varchar(64);primaryKey;index"`
}

// SaveRequestRecord locks blob rows while their files and references are written.
// Cleanup takes the same locks, so it cannot unlink a file a writer is reusing.
func SaveRequestRecord(record *RequestRecord, blobs []RequestRecordBlob, writeFile func(RequestRecordBlob) error) error {
	slices.SortFunc(blobs, func(a, b RequestRecordBlob) int { return strings.Compare(a.ID, b.ID) })
	return DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(record)
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		for _, blob := range blobs {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&blob).Error; err != nil {
				return err
			}
			var locked RequestRecordBlob
			if err := lockForUpdate(tx).Where("id = ?", blob.ID).First(&locked).Error; err != nil {
				return err
			}
			if err := tx.Model(&locked).Updates(map[string]any{"mime_type": blob.MimeType, "size": blob.Size, "search_text": blob.SearchText}).Error; err != nil {
				return err
			}
			if err := writeFile(blob); err != nil {
				return err
			}
			link := RequestRecordBlobLink{RecordID: record.ID, BlobID: blob.ID}
			if err := tx.Create(&link).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ClearRequestRecords deletes every request record and its blob references.
// The stored files are removed by the service layer that owns the storage dir.
func ClearRequestRecords() error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&RequestRecordBlobLink{}).Error; err != nil {
			return err
		}
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&RequestRecordBlob{}).Error; err != nil {
			return err
		}
		return tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&RequestRecord{}).Error
	})
}

func GetRequestRecord(userID int, requestID string) (*RequestRecord, error) {
	var record RequestRecord
	err := DB.Where("user_id = ? AND request_id = ?", userID, requestID).First(&record).Error
	return &record, err
}

func GetRequestRecordByID(id int64, userID int, admin bool) (*RequestRecord, error) {
	query := DB.Where("id = ?", id)
	if !admin {
		query = query.Where("user_id = ?", userID)
	}
	var record RequestRecord
	err := query.First(&record).Error
	return &record, err
}

func GetRequestRecordBlobs(recordID int64) ([]RequestRecordBlob, error) {
	var blobs []RequestRecordBlob
	err := DB.Model(&RequestRecordBlob{}).
		Joins("JOIN request_record_blob_links ON request_record_blob_links.blob_id = request_record_blobs.id").
		Where("request_record_blob_links.record_id = ?", recordID).Find(&blobs).Error
	return blobs, err
}

type RequestRecordFilter struct {
	UserID         int
	OwnerID        int
	Admin          bool
	StartTimestamp int64
	EndTimestamp   int64
	Keyword        string
	Kind           string
	BodyState      string
	ConversationID string
	RunID          string
	TaskID         string
	RequestID      string
	ExcludePolls   bool
}

func (f RequestRecordFilter) apply(tx *gorm.DB) *gorm.DB {
	if f.ExcludePolls {
		tx = tx.Where("request_records.operation <> ?", "poll")
	}
	if !f.Admin {
		tx = tx.Where("request_records.user_id = ?", f.UserID)
	} else if f.OwnerID > 0 {
		tx = tx.Where("request_records.user_id = ?", f.OwnerID)
	}
	if f.StartTimestamp != 0 {
		tx = tx.Where("request_records.created_at >= ?", f.StartTimestamp)
	}
	if f.EndTimestamp != 0 {
		tx = tx.Where("request_records.created_at <= ?", f.EndTimestamp)
	}
	for column, value := range map[string]string{
		"kind": f.Kind, "conversation_id": f.ConversationID,
		"run_id": f.RunID, "task_id": f.TaskID, "request_id": f.RequestID,
	} {
		if value != "" {
			tx = tx.Where("request_records."+column+" = ?", value)
		}
	}
	if f.BodyState != "" {
		now := time.Now().Unix()
		cutoff := now - int64(max(operation_setting.GetRequestRecordSetting().RetentionDays, 1))*86400
		if f.BodyState == "expired" {
			tx = tx.Where("request_records.body_state = ? OR request_records.expires_at <= ? OR request_records.created_at <= ?", "expired", now, cutoff)
		} else {
			tx = tx.Where("request_records.body_state = ? AND request_records.expires_at > ? AND request_records.created_at > ?", f.BodyState, now, cutoff)
		}
	}
	if f.Keyword != "" {
		// An explicit escape character works on SQLite, MySQL and PostgreSQL.
		pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(f.Keyword)) + "%"
		now := time.Now().Unix()
		cutoff := now - int64(max(operation_setting.GetRequestRecordSetting().RetentionDays, 1))*86400
		tx = tx.Where("request_records.request_id = ? OR request_records.task_id = ? OR request_records.conversation_id = ? OR EXISTS (SELECT 1 FROM request_record_blob_links l JOIN request_record_blobs b ON b.id = l.blob_id WHERE l.record_id = request_records.id AND request_records.expires_at > ? AND request_records.created_at > ? AND LOWER(b.search_text) LIKE ? ESCAPE '!')", f.Keyword, f.Keyword, f.Keyword, now, cutoff, pattern)
	}
	return tx
}

func ListRequestRecords(filter RequestRecordFilter, offset, limit int) ([]RequestRecord, int64, error) {
	query := filter.apply(DB.Model(&RequestRecord{}))
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []RequestRecord
	err := query.Order("request_records.created_at DESC, request_records.id DESC").Offset(max(offset, 0)).Limit(min(max(limit, 1), 100)).Find(&records).Error
	return records, total, err
}

// MatchingRequestRecordIDs bridges the independently configured main and log
// databases without a cross-database join. Broad searches fail explicitly.
func MatchingRequestRecordIDs(filter RequestRecordFilter) ([]string, error) {
	var records []RequestRecord
	err := filter.apply(DB.Model(&RequestRecord{})).Select("request_id", "parent_id").Limit(1001).Find(&records).Error
	if err != nil {
		return nil, err
	}
	if len(records) > 1000 {
		return nil, common.NewMessage("Too many matching request records. Narrow the time range or search terms.")
	}
	ids := make([]string, 0, len(records))
	for _, record := range records {
		id := record.RequestID
		if record.Operation == "task_result" || record.ParentID != "" {
			id = record.ParentID
		}
		if id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func ExpireRequestRecords(now int64, retentionDays int) error {
	cutoff := now - int64(max(retentionDays, 1))*24*60*60
	return DB.Transaction(func(tx *gorm.DB) error {
		var ids []int64
		if err := tx.Model(&RequestRecord{}).Where("body_state <> ? AND (expires_at <= ? OR created_at <= ?)", "expired", now, cutoff).Limit(100).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := tx.Where("record_id IN ?", ids).Delete(&RequestRecordBlobLink{}).Error; err != nil {
			return err
		}
		return tx.Model(&RequestRecord{}).Where("id IN ?", ids).Updates(map[string]any{"body_state": "expired", "manifest": ""}).Error
	})
}

func CleanupRequestRecordBlobs(node string, removeFile func(string) error) error {
	var ids []string
	if err := DB.Model(&RequestRecordBlob{}).Where("node_name = ? AND NOT EXISTS (SELECT 1 FROM request_record_blob_links WHERE blob_id = request_record_blobs.id)", node).Limit(200).Pluck("id", &ids).Error; err != nil {
		return err
	}
	for _, id := range ids {
		err := DB.Transaction(func(tx *gorm.DB) error {
			var blob RequestRecordBlob
			if err := lockForUpdate(tx).Where("id = ?", id).First(&blob).Error; err != nil {
				return err
			}
			// A locking read sees references committed after the candidate scan.
			var link RequestRecordBlobLink
			err := lockForUpdate(tx).Where("blob_id = ?", id).First(&link).Error
			if err == nil {
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := removeFile(id); err != nil {
				return err
			}
			return tx.Delete(&blob).Error
		})
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	return nil
}

// AdoptOrphanRequestRecordBlob lets the normal locking cleanup reconcile a file
// left behind by a rolled-back transaction or a process crash.
func AdoptOrphanRequestRecordBlob(id, node string) error {
	var count int64
	if err := DB.Model(&RequestRecordBlob{}).Where("id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		// The same storage directory can survive a container hostname change.
		// Claim only unreferenced local files, so the new node can clean them.
		return DB.Model(&RequestRecordBlob{}).Where("id = ? AND NOT EXISTS (SELECT 1 FROM request_record_blob_links WHERE blob_id = request_record_blobs.id)", id).Update("node_name", node).Error
	}
	blob := RequestRecordBlob{ID: id, NodeName: node}
	return DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&blob).Error
}

func RequestRecordExpired(record *RequestRecord, retentionDays int) bool {
	now := time.Now().Unix()
	return record.BodyState == "expired" || record.ExpiresAt <= now || record.CreatedAt <= now-int64(max(retentionDays, 1))*24*60*60
}
