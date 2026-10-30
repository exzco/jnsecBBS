// 与 Go internal/api 的 DTO 一一对应
export interface Group {
  id: number
  name: string
  color: string
}

export interface User {
  id: number
  username: string
  bio: string
  avatar: string
  groups: Group[]
  isAdmin: boolean
  isMod: boolean
  isBanned: boolean
  discussionsCount: number
  postsCount: number
  createdAt: string
  lastSeenAt: string | null
}

export interface Tag {
  id: number
  name: string
  slug: string
  description: string
  color: string
  position: number
  parentId: number | null
  discussionsCount: number
  canPost: boolean
  canView: boolean
  children?: Tag[]
}

export interface DiscussionSummary {
  id: number
  title: string
  slug: string
  tags: Tag[]
  author: User | null
  commentCount: number
  viewCount: number
  createdAt: string
  lastPostedAt: string
  lastPoster?: string
  isSticky: boolean
  isLocked: boolean
  isHidden: boolean
  hasUnread?: boolean
}

export interface Post {
  id: number
  number: number
  author: User | null
  content: string
  html: string
  likeCount: number
  liked: boolean
  likers?: User[]
  createdAt: string
  editedAt: string | null
  hidden: boolean
  canEdit: boolean
  canHide: boolean
  discussion: { id: number; slug: string }
}

export interface Page {
  page: number
  pages: number
  total: number
}

export interface NotificationItem {
  id: number
  type: "reply" | "mention" | "like"
  sender: User | null
  discussion?: DiscussionSummary
  postId: number
  read: boolean
  createdAt: string
}

export interface SessionInfo {
  readOnly: boolean
  user: User | null
  csrfToken: string
  siteName: string
  registration: "open" | "invite"
  welcomeText?: string
  unread?: number
  realName?: string
  blogUrl?: string
}

export interface HomeCard {
  tag: Tag
  children: Tag[]
  latest: { title: string; url: string; at: string } | null
}

export interface DiscussionViewer {
  user: User
  visitedAt: string
}

export interface DiscussionDetail {
  discussion: DiscussionSummary
  posts: Post[]
  page: Page
  canReply: boolean
  isMod: boolean
  editorTags: Tag[]
  viewers?: DiscussionViewer[]
}

export interface ProfileData {
  user: User
  realName: string
  blogUrl: string
  discTotal: number
  discussions: DiscussionSummary[]
  posts: { post: Post; discussion: DiscussionSummary | null }[]
}

export interface RecentData {
  items: DiscussionSummary[]
  page: Page
}

export interface ActivityDay {
  date: string
  count: number
}

export interface ActivityData {
  year: number
  total: number
  days: ActivityDay[]
  years: number[]
}

export interface PostForm {
  groups: { parent: Tag; children: Tag[] }[]
}

export interface AdminTagNode {
  tag: Tag
  viewIds: number[]
  postIds: number[]
  children: AdminTagNode[]
}

export interface Invite {
  code: string
  createdAt: string
  expiresAt: string | null
  usedAt: string | null
  expired: boolean
}

export interface AdminUserRow {
  user: User
  email: string
  groupIds: number[]
}
