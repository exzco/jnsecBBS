package models

import "time"

// UploadFile 与 flarum_fof_upload_files 兼容的文件记录模型。
type UploadFile struct {
	ID           uint      `gorm:"column:id;primaryKey"`
	ActorID      uint      `gorm:"column:actor_id"`
	BaseName     string    `gorm:"column:base_name"`
	Path         string    `gorm:"column:path"`
	URL          string    `gorm:"column:url"`
	Type         string    `gorm:"column:type"`
	Size         int64     `gorm:"column:size"`
	UploadMethod string    `gorm:"column:upload_method"`
	CreatedAt    time.Time `gorm:"column:created_at"`
	UUID         string    `gorm:"column:uuid"`
	Tag          string    `gorm:"column:tag"`
}

func (UploadFile) TableName() string { return "flarum_fof_upload_files" }
