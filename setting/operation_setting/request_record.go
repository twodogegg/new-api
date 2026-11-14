package operation_setting

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type RequestRecordSetting struct {
	Enabled       bool `json:"enabled"`
	RetentionDays int  `json:"retention_days"`
	MaxBodyMB     int  `json:"max_body_mb"`
}

var requestRecordSetting = RequestRecordSetting{RetentionDays: 7, MaxBodyMB: 16}

func init() {
	config.GlobalConfig.Register("request_record_setting", &requestRecordSetting)
}

func GetRequestRecordSetting() *RequestRecordSetting {
	copy := RequestRecordSetting{RetentionDays: 7, MaxBodyMB: 16}
	_ = config.GlobalConfig.Snapshot("request_record_setting", &copy)
	return &copy
}

func ValidateRequestRecordOption(key, value string) error {
	if key != "request_record_setting.enabled" && key != "request_record_setting.retention_days" && key != "request_record_setting.max_body_mb" {
		return common.NewMessage("Invalid request recording setting")
	}
	if key == "request_record_setting.enabled" {
		if value != "true" && value != "false" {
			return common.NewMessage("Invalid request recording setting")
		}
		return nil
	}
	n, err := strconv.Atoi(value)
	maximum := 3650
	if key == "request_record_setting.max_body_mb" {
		maximum = 64
	}
	if err != nil || n < 1 || n > maximum {
		return common.NewMessage("Invalid request recording setting")
	}
	return nil
}
