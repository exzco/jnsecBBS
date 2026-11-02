import { Link, useNavigate } from "react-router"
import { Pin, Lock, Trash2 } from "lucide-react"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import { cn } from "cn"
import type { DiscussionSummary, Tag, User } from "@/lib/types"
import { timeAgo } from "@/lib/format"

export function UserAvatar({ user, className }: { user: User | null; className?: string }) {
  return (
    <Avatar className={cn("size-9", className)}>
      <AvatarImage src={user?.avatar ?? "/avatar/_unknown.svg"} alt={user?.username ?? "?"} />
      <AvatarFallback>{user?.username?.[0] ?? "?"}</AvatarFallback>
    </Avatar>
  )
}

export function TagChip({
  tag,
  onColor = false,
  className,
}: {
  tag: Tag
  onColor?: boolean
  className?: string
}) {
  const rawColor = tag.color?.trim()
  const color = rawColor && /^#[0-9a-fA-F]{3,8}$/.test(rawColor) ? rawColor : "#00838f"

  if (onColor) {
    return (
      <Link
        to={`/?board=${encodeURIComponent(tag.slug)}`}
        onClick={(e) => e.stopPropagation()}
        className={cn(
          "inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium transition-colors bg-white/20 text-white hover:bg-white/35",
          className,
        )}
      >
        {tag.name}
      </Link>
    )
  }

  return (
    <Link
      to={`/?board=${encodeURIComponent(tag.slug)}`}
      onClick={(e) => e.stopPropagation()}
      style={{
        backgroundColor: `${color}18`,
        color: color,
        borderColor: `${color}35`,
      }}
      className={cn(
        "inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-medium transition-all hover:brightness-110",
        className,
      )}
    >
      <span className="mr-1.5 size-1.5 rounded-full shrink-0" style={{ backgroundColor: color }} />
      {tag.name}
    </Link>
  )
}

export function GroupBadges({ user }: { user: User }) {
  return (
    <>
      {user.groups
        .filter((g) => g.id !== 2)
        .map((g) => (
          <Badge
            key={g.id}
            variant="outline"
            className="text-[10px] px-1.5 py-0 font-medium"
            style={g.color ? { color: g.color, borderColor: `${g.color}66` } : undefined}
          >
            {g.name}
          </Badge>
        ))}
    </>
  )
}

// 高密度讨论条目：参考最新帖子紧凑风格（上方标题，下方左侧作者名称，下方右侧 xxx 回复于 xx 前）
export function DiscussionRowCompact({
  d,
  onOpen,
}: {
  d: DiscussionSummary
  onOpen?: (id: number) => void
}) {
  const navigate = useNavigate()
  const open = () => (onOpen ? onOpen(d.id) : navigate(`/d/${d.id}-${d.slug}`))
  const mainTag = d.tags[0]
  const replier = d.lastPoster || d.author?.username || "未知"
  const ago = timeAgo(d.lastPostedAt)

  return (
    <article
      role="link"
      tabIndex={0}
      onClick={open}
      onKeyDown={(e) => e.key === "Enter" && open()}
      className="group relative flex flex-col justify-between px-3.5 py-2.5 sm:px-4 sm:py-3 transition-colors duration-150 hover:bg-muted/50 cursor-pointer focus-visible:outline-none focus-visible:bg-muted/60"
    >
      {/* 1. 上方：标题与状态徽标 */}
      <div className="flex items-start justify-between gap-2.5">
        <h2 className="flex-1 min-w-0 text-sm sm:text-[15px] font-semibold tracking-tight text-foreground group-hover:text-primary transition-colors line-clamp-1 leading-snug">
          {d.hasUnread && (
            <span
              className="mr-1.5 inline-block size-2 rounded-full bg-blue-600 ring-2 ring-blue-500/20 align-middle shrink-0"
              title="有未读更新"
            />
          )}
          {d.isSticky && (
            <span className="mr-1.5 inline-flex items-center gap-0.5 rounded bg-amber-500/15 px-1.5 py-0.2 text-[10px] font-semibold text-amber-700 dark:text-amber-400 align-middle shrink-0">
              <Pin className="size-2.5" /> 置顶
            </span>
          )}
          {d.isLocked && (
            <span className="mr-1.5 inline-flex items-center gap-0.5 rounded bg-zinc-500/15 px-1.5 py-0.2 text-[10px] font-semibold text-zinc-700 dark:text-zinc-400 align-middle shrink-0">
              <Lock className="size-2.5" /> 锁定
            </span>
          )}
          {d.isHidden && (
            <span className="mr-1.5 inline-flex items-center gap-0.5 rounded bg-red-500/15 px-1.5 py-0.2 text-[10px] font-semibold text-red-600 dark:text-red-400 align-middle shrink-0">
              <Trash2 className="size-2.5" /> 已删除
            </span>
          )}
          <span>{d.title}</span>
        </h2>
        {mainTag && (
          <span className="hidden sm:inline-flex shrink-0">
            <TagChip tag={mainTag} className="text-[10px] py-0 px-2 leading-tight" />
          </span>
        )}
      </div>

      {/* 2. 下方：左侧作者名称，右侧 xxx 回复于 xx 前 */}
      <div className="mt-1.5 flex items-center justify-between text-xs text-muted-foreground leading-none">
        {/* 下方左侧：作者名称 */}
        <Link
          to={`/u/${d.author?.username ?? ""}`}
          onClick={(e) => e.stopPropagation()}
          className="flex items-center gap-1.5 font-medium text-foreground/80 hover:text-primary hover:underline truncate max-w-[45%]"
        >
          <UserAvatar user={d.author} className="size-4.5 ring-1 ring-border/40 shrink-0" />
          <span className="truncate">{d.author?.username}</span>
        </Link>

        {/* 下方右侧：xxx 回复于 xx 前 */}
        <div className="flex items-center gap-1 text-xs text-muted-foreground/80 shrink-0">
          <span className="truncate">
            <span className="font-medium text-foreground/75">{replier}</span>
            {" 回复于 "}
            <time title={d.lastPostedAt ? new Date(d.lastPostedAt).toLocaleString() : ""}>
              {ago || "刚刚"}
            </time>
          </span>
        </div>
      </div>
    </article>
  )
}

export function DiscussionRow({ d }: { d: DiscussionSummary }) {
  return <DiscussionRowCompact d={d} />
}
