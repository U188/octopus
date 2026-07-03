package model

import "time"

type AuditLog struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Action    string    `json:"action" gorm:"size:64;not null;index:idx_audit_action_created,priority:1"`
	Status    string    `json:"status" gorm:"size:16;not null;index"`
	Actor     string    `json:"actor" gorm:"size:128"`
	IP        string    `json:"ip" gorm:"size:64"`
	UserAgent string    `json:"user_agent" gorm:"size:512"`
	Method    string    `json:"method" gorm:"size:16"`
	Path      string    `json:"path" gorm:"size:256"`
	Detail    string    `json:"detail" gorm:"type:text"`
	Error     string    `json:"error" gorm:"type:text"`
	CreatedAt time.Time `json:"created_at" gorm:"index:idx_audit_action_created,priority:2;index"`
}
