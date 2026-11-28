package models

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"strings"
	"time"

	"github.com/exzco/bbs/internal/util"
)

// 字段类型按生产库 information_schema 实测对齐（2026-10 采集），
// 见 user.go 注释——保证与 PHP 端共享表时的读写安全。

type Discussion struct {
	ID               uint       `gorm:"column:id;primaryKey"`
	Title            string     `gorm:"column:title;type:varchar(200)"`
	CommentCount     int        `gorm:"column:comment_count"`
	ParticipantCount int        `gorm:"column:participant_count;type:int unsigned"`
	PostNumberIndex  int        `gorm:"column:post_number_index;type:int unsigned"`
	CreatedAt        time.Time  `gorm:"column:created_at"`
	UserID           *uint      `gorm:"column:user_id"`
	User             User       `gorm:"foreignKey:UserID"`
	FirstPostID      *uint      `gorm:"column:first_post_id"`
	LastPostedAt     *time.Time `gorm:"column:last_posted_at"`
	LastPostID       *uint      `gorm:"column:last_post_id"`
	LastPostNumber   *int       `gorm:"column:last_post_number;type:int unsigned"`
	LastPostUserID   *uint      `gorm:"column:last_posted_user_id"`
	LastPostUser     *User      `gorm:"foreignKey:LastPostUserID"`
	HiddenAt         *time.Time `gorm:"column:hidden_at"`
	HiddenUserID     *uint      `gorm:"column:hidden_user_id"`
	SlugCol          string     `gorm:"column:slug;type:varchar(255)"`
	IsPrivate        bool       `gorm:"column:is_private"`
	IsApproved       bool       `gorm:"column:is_approved"`
	IsLocked         bool       `gorm:"column:is_locked"`
	IsSticky         bool       `gorm:"column:is_sticky"`
	ViewCount        int        `gorm:"column:view_count"`
	Tags             []Tag      `gorm:"many2many:flarum_discussion_tag;foreignKey:ID;joinForeignKey:DiscussionID;joinReferences:TagID"`
}

func (Discussion) TableName() string { return "flarum_discussions" }

// Slug 优先用库内 slug 列（PHP 端生成的历史值，保持旧链接完全一致）；
// 空则按标题运行时生成。
func (d *Discussion) Slug() string {
	if d.SlugCol != "" {
		return d.SlugCol
	}
	return util.MakeSlug(d.Title)
}

func (d *Discussion) IsHidden() bool { return d.HiddenAt != nil }

// LastPostedAtOrZero 供 DTO 使用：可空时间零值兜底。
func (d *Discussion) LastPostedAtOrZero() time.Time {
	if d.LastPostedAt != nil {
		return *d.LastPostedAt
	}
	return d.CreatedAt
}

type Post struct {
	ID           uint        `gorm:"column:id;primaryKey"`
	DiscussionID uint        `gorm:"column:discussion_id"`
	Number       *int        `gorm:"column:number;type:int unsigned"`
	CreatedAt    time.Time   `gorm:"column:created_at"`
	UserID       *uint       `gorm:"column:user_id"`
	User         User        `gorm:"foreignKey:UserID"`
	Type         string      `gorm:"column:type;type:varchar(100)"`  // 仅 'comment' 参与内容展示
	Content      string      `gorm:"column:content;type:mediumtext"` // s9e XML; historical Go JSON is also readable
	EditedAt     *time.Time  `gorm:"column:edited_at"`
	EditedUserID *uint       `gorm:"column:edited_user_id"`
	HiddenAt     *time.Time  `gorm:"column:hidden_at"`
	HiddenUserID *uint       `gorm:"column:hidden_user_id"`
	IPAddress    string      `gorm:"column:ip_address;type:varchar(45)"`
	IsPrivate    bool        `gorm:"column:is_private"`
	IsApproved   bool        `gorm:"column:is_approved"`
	LikeCount    int         `gorm:"-"` // 实时 COUNT（flarum_post_likes），不落库
	Likers       []User      `gorm:"-"` // 实时最近点赞用户列表
	Discussion   *Discussion `gorm:"foreignKey:DiscussionID"`
}

func (Post) TableName() string { return "flarum_posts" }

// Markdown restores source text from legacy XML, earlier Go JSON, or plain text.
func (p *Post) Markdown() string {
	if p.Type != "comment" {
		return ""
	}
	return decodePostContent(p.Content)
}

func (p *Post) SetMarkdown(md string) {
	p.Content = encodePostContent(md)
}

// decodePostContent reverses s9e TextFormatter's XML representation. Formatting
// markers (s/e tags) are part of the original source and must be retained.
// Like s9e's Unparser, concatenate text without adding newlines for XML tags:
// the original newlines already exist next to <br/> and paragraph boundaries.
// Never interpret arbitrary Markdown as XML or silently drop malformed input.
func decodePostContent(raw string) string {
	if strings.HasPrefix(raw, "<r>") || strings.HasPrefix(raw, "<t>") {
		decoder := xml.NewDecoder(strings.NewReader(raw))
		var source strings.Builder
		pendingBR := false
		pendingQuoteBreak := false
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				return source.String()
			}
			if err != nil {
				return raw
			}
			switch tok := token.(type) {
			case xml.StartElement:
				if tok.Name.Local == "br" {
					pendingBR = true
				}
			case xml.EndElement:
				if tok.Name.Local == "QUOTE" {
					pendingQuoteBreak = true
				}
			case xml.CharData:
				if pendingBR {
					if !strings.HasPrefix(string(tok), "\n") && !strings.HasSuffix(source.String(), "\n") {
						source.WriteByte('\n')
					}
					pendingBR = false
				}
				if pendingQuoteBreak {
					if !strings.HasSuffix(source.String(), "\n\n") {
						if strings.HasSuffix(source.String(), "\n") {
							source.WriteByte('\n')
						} else {
							source.WriteString("\n\n")
						}
					}
					pendingQuoteBreak = false
				}
				source.Write(tok)
			}
		}
	}
	// Earlier Go versions stored JSON. Only recognize an actual text field;
	// valid JSON examples such as {"answer":42} are ordinary post content.
	var old struct {
		Text *string `json:"text"`
	}
	if json.Unmarshal([]byte(raw), &old) == nil && old.Text != nil {
		return *old.Text
	}
	return raw
}

// encodePostContent uses s9e's plain-text XML envelope. This preserves the complete
// Markdown source for either editor and is readable by the PHP renderer. PHP
// displays it as text until its formatter reparses it; Go renders Markdown.
func encodePostContent(source string) string {
	var out strings.Builder
	out.WriteString("<t>")
	_ = xml.EscapeText(&out, []byte(source))
	out.WriteString("</t>")
	return out.String()
}

// PostNumber 返回楼层号（0 表示无楼层，如 discussionRenamed 等系统帖）。
func (p *Post) PostNumber() int {
	if p.Number == nil {
		return 0
	}
	return *p.Number
}

type PostLike struct {
	PostID    uint      `gorm:"column:post_id;primaryKey"`
	UserID    uint      `gorm:"column:user_id;primaryKey"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (PostLike) TableName() string { return "flarum_post_likes" }

// DiscussionUser 记录用户在讨论中的已读状态与进度（兼容 Flarum 原生 flarum_discussion_user 表）。
type DiscussionUser struct {
	UserID             uint       `gorm:"column:user_id;primaryKey"`
	DiscussionID       uint       `gorm:"column:discussion_id;primaryKey"`
	LastReadAt         *time.Time `gorm:"column:last_read_at"`
	LastReadPostNumber *int       `gorm:"column:last_read_post_number;type:int unsigned"`
	Subscription       *string    `gorm:"column:subscription;type:varchar(50)"`
}

func (DiscussionUser) TableName() string { return "flarum_discussion_user" }

// DiscussionReadPosition 记录用户在讨论中实际阅读停留的楼层进度（按账户跨端同步）。
type DiscussionReadPosition struct {
	UserID       uint      `gorm:"column:user_id;primaryKey"`
	DiscussionID uint      `gorm:"column:discussion_id;primaryKey"`
	PostID       uint      `gorm:"column:post_id"`
	PostNumber   int       `gorm:"column:post_number"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
}

func (DiscussionReadPosition) TableName() string { return "bbs_discussion_read_positions" }

// DiscussionView 记录讨论帖子的访问与浏览历史（兼容 Flarum 原生 flarum_discussion_views 表）。
type DiscussionView struct {
	ID           uint      `gorm:"column:id;primaryKey;autoIncrement"`
	UserID       *uint     `gorm:"column:user_id"`
	User         *User     `gorm:"foreignKey:UserID"`
	DiscussionID uint      `gorm:"column:discussion_id"`
	IP           string    `gorm:"column:ip;type:varchar(16)"`
	VisitedAt    time.Time `gorm:"column:visited_at"`
}

func (DiscussionView) TableName() string { return "flarum_discussion_views" }

const TableDiscussionTag = "flarum_discussion_tag"

// DiscussionTag 映射讨论与版块的关联表 flarum_discussion_tag。
type DiscussionTag struct {
	DiscussionID uint `gorm:"column:discussion_id;primaryKey"`
	TagID        uint `gorm:"column:tag_id;primaryKey"`
}

func (DiscussionTag) TableName() string { return TableDiscussionTag }

// ContentInput 通用 Markdown 内容请求（预览、回帖、编辑）。
type ContentInput struct {
	Content string `json:"content"`
}

// ReadProgressInput 阅读进度保存请求。
type ReadProgressInput struct {
	PostID     uint `json:"postId"`
	PostNumber int  `json:"postNumber"`
}

// CreateDiscussionInput 发帖请求。
type CreateDiscussionInput struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	TagIDs  []uint `json:"tagIds"`
}

// ModerateInput 讨论管理操作请求。
type ModerateInput struct {
	Action string `json:"action"`
	Title  string `json:"title"`
	TagIDs []uint `json:"tagIds"`
}
