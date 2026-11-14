// 附件上传记录与存储：与 flarum_fof_upload_files 表兼容。
package service

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/exzco/bbs/internal/models"
	"github.com/google/uuid"
)

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

// UploadResult 上传完成返回（与前端约定）。
type UploadResult struct {
	URL      string
	UUID     string
	Name     string
	Size     int64
	IsImage  bool
	Markdown string
}

var blockedExts = map[string]bool{
	".exe": true, ".bat": true, ".cmd": true, ".sh": true,
	".php": true, ".phtml": true, ".jsp": true, ".asp": true,
	".aspx": true, ".cgi": true, ".py": true, ".pl": true,
}

// SaveUpload 校验扩展名、落盘到 data/assets/files/<日期>/ 并写入上传记录。
func (s *Service) SaveUpload(u *models.User, filename, contentType string, size int64, r io.Reader) (UploadResult, *Error) {
	ext := strings.ToLower(filepath.Ext(filename))
	if blockedExts[ext] {
		return UploadResult{}, Err(http.StatusBadRequest, "出于安全原因，禁止上传可执行或脚本文件。")
	}
	dateDir := time.Now().Format("2006-01-02")
	destDir := filepath.Join(s.cfg.DataDir, "assets", "files", dateDir)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return UploadResult{}, Err(http.StatusInternalServerError, "创建存储目录失败。")
	}
	hexPart, _ := randHex(6)
	savedName := fmt.Sprintf("%d-%s%s", time.Now().Unix(), hexPart, ext)
	f, err := os.Create(filepath.Join(destDir, savedName))
	if err != nil {
		return UploadResult{}, Err(http.StatusInternalServerError, "保存文件失败。")
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return UploadResult{}, Err(http.StatusInternalServerError, "保存文件失败。")
	}

	relPath := dateDir + "/" + savedName
	publicURL := "/assets/files/" + relPath
	fileUUID := uuid.New().String()
	isImage := ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp" || ext == ".svg" || ext == ".bmp"
	tag := "file"
	if isImage {
		tag = "image-preview"
	}
	if err := s.db.Create(&UploadFile{
		ActorID: u.ID, BaseName: filename, Path: relPath, URL: publicURL,
		Type: contentType, Size: size, UploadMethod: "local",
		CreatedAt: time.Now(), UUID: fileUUID, Tag: tag,
	}).Error; err != nil {
		return UploadResult{}, Err(http.StatusInternalServerError, "保存文件失败。")
	}

	var md string
	if isImage {
		md = fmt.Sprintf("![%s](%s)", filename, publicURL)
	} else {
		md = fmt.Sprintf("[📎 %s (%s)](%s)", filename, formatBytes(size), publicURL)
	}
	return UploadResult{URL: publicURL, UUID: fileUUID, Name: filename, Size: size, IsImage: isImage, Markdown: md}, nil
}

// FindUpload 按 UUID 查找上传记录（附件下载入口）。
func (s *Service) FindUpload(id string) (*UploadFile, *Error) {
	var rec UploadFile
	if err := s.db.Where("uuid = ?", id).First(&rec).Error; err != nil {
		return nil, Err(http.StatusNotFound, "文件不存在。")
	}
	return &rec, nil
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
