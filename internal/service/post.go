// 帖子（楼层）级操作：编辑、隐藏、点赞。
package service

import (
	"net/http"
	"strings"
	"time"

	"github.com/exzco/bbs/internal/auth"
	"github.com/exzco/bbs/internal/models"
)

// loadPost 加载帖子并做可见性检查。
func (s *Service) loadPost(u *models.User, id uint) (*models.Post, *Error) {
	var p models.Post
	if err := s.db.First(&p, id).Error; err != nil {
		return nil, Err(http.StatusNotFound, "该帖子不存在。")
	}
	var d models.Discussion
	if err := visibleDiscussions(s.db, u).Preload("Tags").First(&d, p.DiscussionID).Error; err != nil {
		return nil, Err(http.StatusNotFound, "该帖子不存在。")
	}
	s.FillTagPerms(d.Tags)
	for _, t := range d.Tags {
		if !auth.CanViewTag(u, &t) {
			return nil, Err(http.StatusForbidden, "你没有查看该帖子的权限。")
		}
	}
	isMod := u.IsModerator()
	isAuthor := p.UserID != nil && *p.UserID == u.ID
	if d.HiddenAt != nil && !isMod && !isAuthor {
		return nil, Err(http.StatusNotFound, "该帖子不存在或已被删除。")
	}
	if p.IsPrivate || p.Type != "comment" || (!p.IsApproved && !isMod) {
		return nil, Err(http.StatusNotFound, "该帖子不存在。")
	}
	return &p, nil
}

// PostEditDiscussion 编辑页所属讨论信息。
type PostEditDiscussion struct {
	ID    uint   `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// PostEditData 编辑页数据。
type PostEditData struct {
	ID         uint               `json:"id"`
	Page       int                `json:"page"`
	Content    string             `json:"content"`
	Discussion PostEditDiscussion `json:"discussion"`
}

// PostEdit 编辑页：定位回复所在评论页（按评论位置而非楼层号）。
func (s *Service) PostEdit(u *models.User, postID uint) (PostEditData, *Error) {
	p, err := s.loadPost(u, postID)
	if err != nil {
		return PostEditData{}, err
	}
	if !auth.CanEditPost(u, p) {
		return PostEditData{}, Err(http.StatusForbidden, "你只能编辑自己的回复。")
	}
	var d models.Discussion
	if err := s.db.Select("id, slug, title").First(&d, p.DiscussionID).Error; err != nil {
		return PostEditData{}, Err(http.StatusInternalServerError, "加载失败，请重试。")
	}
	// 楼层号包含系统事件和已移除的回复，不能直接用楼层号推算分页。
	var position int64
	query := visibleComments(s.db, u, d.ID)
	if p.Number != nil {
		query = query.Where("number <= ? OR number IS NULL", *p.Number)
	} else {
		query = query.Where("number IS NULL")
	}
	if err := query.Count(&position).Error; err != nil {
		return PostEditData{}, Err(http.StatusInternalServerError, "加载失败，请重试。")
	}
	page := 1
	if position > 0 {
		page = int((position-1)/PerPagePosts) + 1
	}
	return PostEditData{
		ID:      p.ID,
		Page:    page,
		Content: p.Markdown(),
		Discussion: PostEditDiscussion{
			ID: d.ID, Slug: d.Slug(), Title: d.Title,
		},
	}, nil
}

// EditPost 编辑回复内容（更新 edited 元数据并发送 @提及 通知）。
func (s *Service) EditPost(u *models.User, postID uint, content string) (time.Time, *Error) {
	if strings.TrimSpace(content) == "" {
		return time.Time{}, Err(http.StatusBadRequest, "内容不能为空。")
	}
	p, err := s.loadPost(u, postID)
	if err != nil {
		return time.Time{}, err
	}
	if !auth.CanEditPost(u, p) {
		return time.Time{}, Err(http.StatusForbidden, "你只能编辑自己的回复。")
	}
	now := time.Now()
	edited := now
	p.SetMarkdown(content)
	if err := s.db.Model(p).Updates(map[string]any{"edited_at": edited, "edited_user_id": u.ID, "content": p.Content}).Error; err != nil {
		return time.Time{}, Err(http.StatusInternalServerError, "保存失败，请重试。")
	}
	authorID := uint(0)
	if p.UserID != nil {
		authorID = *p.UserID
	}
	s.notifyMentions(s.db, content, authorID, p.DiscussionID, p.ID)
	return edited, nil
}

// HidePost 隐藏/恢复回复；主楼隐藏状态同步到讨论。
func (s *Service) HidePost(u *models.User, postID uint) (bool, *Error) {
	p, err := s.loadPost(u, postID)
	if err != nil {
		return false, err
	}
	if (p.UserID == nil || *p.UserID != u.ID) && !u.IsModerator() {
		return false, Err(http.StatusForbidden, "无权隐藏该回复。")
	}
	var newHiddenAt *time.Time
	updates := map[string]any{"hidden_at": nil, "hidden_user_id": nil}
	if p.HiddenAt == nil {
		now := time.Now()
		newHiddenAt = &now
		updates = map[string]any{"hidden_at": newHiddenAt, "hidden_user_id": u.ID}
	}
	if err := s.db.Model(&models.Post{}).Where("id = ?", p.ID).Updates(updates).Error; err != nil {
		return false, Err(http.StatusInternalServerError, "操作失败，请重试。")
	}

	// 若操作的是主楼/首帖，同步设置讨论的隐藏状态（隐藏人/清空值与帖子一致）
	var d models.Discussion
	if err := s.db.Select("id", "first_post_id").First(&d, p.DiscussionID).Error; err == nil {
		if (p.Number != nil && *p.Number == 1) || (d.FirstPostID != nil && p.ID == *d.FirstPostID) {
			if err := s.db.Model(&models.Discussion{}).Where("id = ?", d.ID).Updates(updates).Error; err != nil {
				return false, Err(http.StatusInternalServerError, "操作失败，请重试。")
			}
		}
	}

	return newHiddenAt != nil, nil
}

// LikeResult 点赞切换返回。
type LikeResult struct {
	Liked     bool      `json:"liked"`
	LikeCount int64     `json:"likeCount"`
	Likers    []UserDTO `json:"likers"`
}

// ToggleLike 切换当前用户对帖子的点赞。
func (s *Service) ToggleLike(u *models.User, postID uint) (LikeResult, *Error) {
	p, err := s.loadPost(u, postID)
	if err != nil {
		return LikeResult{}, err
	}
	var like models.PostLike
	liked := false
	if err := s.db.Where("post_id = ? AND user_id = ?", p.ID, u.ID).First(&like).Error; err == nil {
		if err := s.db.Delete(&like).Error; err != nil {
			return LikeResult{}, Err(http.StatusInternalServerError, "操作失败，请重试。")
		}
	} else {
		if err := s.db.Create(&models.PostLike{PostID: p.ID, UserID: u.ID}).Error; err != nil {
			return LikeResult{}, Err(http.StatusInternalServerError, "操作失败。")
		}
		liked = true
		if p.UserID != nil {
			if err := notify(s.db, *p.UserID, u.ID, "like", p.DiscussionID, p.ID); err != nil {
				return LikeResult{}, Err(http.StatusInternalServerError, "操作失败，请重试。")
			}
		}
	}
	var n int64
	s.db.Model(&models.PostLike{}).Where("post_id = ?", p.ID).Count(&n)
	return LikeResult{Liked: liked, LikeCount: n, Likers: s.loadPostLikers(p.ID)}, nil
}
