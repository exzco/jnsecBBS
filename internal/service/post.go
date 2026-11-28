// 帖子（楼层）级操作：编辑、点赞与正文渲染。
package service

import (
	"net/http"
	"strings"
	"time"

	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/util"
)

// canEditPost 仅帖子作者本人或版主可编辑。
func canEditPost(u *models.User, p *models.Post) bool {
	if u == nil {
		return false
	}
	return u.IsModerator() || (p.UserID != nil && *p.UserID == u.ID)
}

// loadPost 加载评论帖子。
func (s *Service) loadPost(id uint) (*models.Post, *Error) {
	var p models.Post
	if err := s.db.First(&p, id).Error; err != nil {
		return nil, Err(http.StatusNotFound, "该帖子不存在。")
	}
	if p.Type != "comment" {
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
	p, err := s.loadPost(postID)
	if err != nil {
		return PostEditData{}, err
	}
	if !canEditPost(u, p) {
		return PostEditData{}, Err(http.StatusForbidden, "你只能编辑自己的回复。")
	}
	var d models.Discussion
	if err := s.db.Select("id, slug, title").First(&d, p.DiscussionID).Error; err != nil {
		return PostEditData{}, Err(http.StatusInternalServerError, "加载失败，请重试。")
	}
	// 楼层号包含系统事件等非评论行，不能直接用楼层号推算分页。
	var position int64
	query := s.db.Model(&models.Post{}).Where("discussion_id = ? AND type = ?", d.ID, "comment")
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
	p, err := s.loadPost(postID)
	if err != nil {
		return time.Time{}, err
	}
	if !canEditPost(u, p) {
		return time.Time{}, Err(http.StatusForbidden, "你只能编辑自己的回复。")
	}
	now := time.Now()
	p.SetMarkdown(content)
	if err := s.db.Model(p).Updates(map[string]any{"edited_at": now, "edited_user_id": u.ID, "content": p.Content}).Error; err != nil {
		return time.Time{}, Err(http.StatusInternalServerError, "保存失败，请重试。")
	}
	authorID := uint(0)
	if p.UserID != nil {
		authorID = *p.UserID
	}
	s.notifyMentions(s.db, content, authorID, p.DiscussionID, p.ID)
	return now, nil
}

// LikeResult 点赞切换返回。
type LikeResult struct {
	Liked     bool      `json:"liked"`
	LikeCount int64     `json:"likeCount"`
	Likers    []UserDTO `json:"likers"`
}

// ToggleLike 切换当前用户对帖子的点赞。
func (s *Service) ToggleLike(u *models.User, postID uint) (LikeResult, *Error) {
	p, err := s.loadPost(postID)
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

type PostDTO struct {
	ID         uint       `json:"id"`
	Number     int        `json:"number"`
	Author     *UserDTO   `json:"author"`
	Content    string     `json:"content"` // Markdown 原文
	HTML       string     `json:"html"`    // 实时渲染（消毒后）
	LikeCount  int        `json:"likeCount"`
	Liked      bool       `json:"liked"`
	Likers     []UserDTO  `json:"likers"`
	CreatedAt  time.Time  `json:"createdAt"`
	EditedAt   *time.Time `json:"editedAt"`
	CanEdit    bool       `json:"canEdit"`
	Discussion struct {
		ID   uint   `json:"id"`
		Slug string `json:"slug"`
	} `json:"discussion"`
}

func (s *Service) postDTO(p *models.Post, likedSet map[uint]bool, u *models.User) PostDTO {
	likers := make([]UserDTO, 0, len(p.Likers))
	for i := range p.Likers {
		if d := s.UserDTO(&p.Likers[i]); d != nil {
			likers = append(likers, *d)
		}
	}
	source := p.Markdown()
	html, _ := util.Render(source, s.mentionedUserID)
	dto := PostDTO{
		ID: p.ID, Number: p.PostNumber(), Author: s.UserDTO(&p.User),
		Content: source, HTML: html,
		LikeCount: p.LikeCount, Liked: likedSet[p.ID], Likers: likers,
		CreatedAt: p.CreatedAt, EditedAt: p.EditedAt,
		CanEdit: !s.cfg.ReadOnly && canEditPost(u, p),
	}
	dto.Discussion.ID = p.DiscussionID
	return dto
}

// loadLikes 批量填充帖子的点赞数、点赞用户列表（每帖最近 20 条）与当前用户是否已赞。
func (s *Service) loadLikes(posts []models.Post, viewer *models.User) map[uint]bool {
	liked := map[uint]bool{}
	if len(posts) == 0 {
		return liked
	}
	ids := make([]uint, 0, len(posts))
	for i := range posts {
		ids = append(ids, posts[i].ID)
	}
	var likes []models.PostLike
	s.db.Where("post_id IN ?", ids).Order("created_at DESC, user_id DESC").Find(&likes)
	counts := map[uint]int{}
	likers := map[uint][]uint{}
	userIDs := []uint{}
	seen := map[uint]bool{}
	for _, l := range likes {
		counts[l.PostID]++
		if len(likers[l.PostID]) < 20 {
			likers[l.PostID] = append(likers[l.PostID], l.UserID)
			if !seen[l.UserID] {
				seen[l.UserID] = true
				userIDs = append(userIDs, l.UserID)
			}
		}
	}
	userMap := s.usersByIDs(userIDs)
	for i := range posts {
		posts[i].LikeCount = counts[posts[i].ID]
		posts[i].Likers = []models.User{}
		for _, uid := range likers[posts[i].ID] {
			if u, ok := userMap[uid]; ok {
				posts[i].Likers = append(posts[i].Likers, *u)
			}
		}
	}
	if viewer != nil {
		var mine []models.PostLike
		s.db.Where("post_id IN ? AND user_id = ?", ids, viewer.ID).Find(&mine)
		for _, l := range mine {
			liked[l.PostID] = true
		}
	}
	return liked
}

// loadPostLikers 获取单帖最近点赞用户列表（用于点赞切换后即时返回）。
func (s *Service) loadPostLikers(postID uint) []UserDTO {
	var likes []models.PostLike
	if err := s.db.Where("post_id = ?", postID).Order("created_at DESC, user_id DESC").Limit(20).Find(&likes).Error; err != nil || len(likes) == 0 {
		return []UserDTO{}
	}
	userIDs := make([]uint, 0, len(likes))
	for _, l := range likes {
		userIDs = append(userIDs, l.UserID)
	}
	userMap := s.usersByIDs(userIDs)
	likers := make([]UserDTO, 0, len(likes))
	for _, l := range likes {
		if u, ok := userMap[l.UserID]; ok {
			if dto := s.UserDTO(u); dto != nil {
				likers = append(likers, *dto)
			}
		}
	}
	return likers
}
