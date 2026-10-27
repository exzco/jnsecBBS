import { Link, useNavigate } from "react-router"
import { MessageSquare } from "lucide-react"
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

export function TagChip({ tag, onColor = false }: { tag: Tag; onColor?: boolean }) {
  return (
    <Link
      to={`/t/${tag.slug}`}
      className={cn(
        "inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium transition-colors",
        onColor
          ? "bg-white/20 text-white hover:bg-white/35"
          : "border bg-secondary/60 text-secondary-foreground hover:bg-secondary",
      )}
    >
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
            className="text-[10px] px-1.5 py-0"
            style={g.color ? { color: g.color, borderColor: `${g.color}66` } : undefined}
          >
            {g.name}
          </Badge>
        ))}
    </>
  )
}

// 紧凑行（首页最新流）：整行可点进入详情；onOpen 存在时交给外层内嵌渲染
export function DiscussionRowCompact({
  d,
  onOpen,
}: {
  d: DiscussionSummary
  onOpen?: (id: number) => void
}) {
  const navigate = useNavigate()
  const open = () => (onOpen ? onOpen(d.id) : navigate(`/d/${d.id}-${d.slug}`))
  return (
    <div
      role="link"
      tabIndex={0}
      onClick={open}
      onKeyDown={(e) => e.key === "Enter" && open()}
      className="flex cursor-pointer items-center gap-3 border-b border-border/50 px-2 py-2 last:border-b-0 hover:bg-muted/40"
    >
      <Link
        to={`/u/${d.author?.username ?? ""}`}
        onClick={(e) => e.stopPropagation()}
        className="shrink-0"
      >
        <UserAvatar user={d.author} className="size-7" />
      </Link>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          {d.hasUnread && (
            <span className="size-2 rounded-full bg-primary shrink-0" title="有未读更新" />
          )}
          {d.isSticky && (
            <span className="shrink-0 rounded bg-orange-500/10 px-1 py-px text-[10px] font-semibold text-orange-600 dark:text-orange-400">
              置顶
            </span>
          )}
          {d.isLocked && (
            <span className="shrink-0 rounded bg-blue-500/10 px-1 py-px text-[10px] font-semibold text-blue-600 dark:text-blue-400">
              锁
            </span>
          )}
          <span className="truncate text-sm font-medium text-foreground">{d.title}</span>
        </div>
        <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
          <span className="max-w-28 shrink-0 truncate">{d.author?.username}</span>
          {d.tags.slice(0, 2).map((t) => (
            <span
              key={t.id}
              className="hidden shrink-0 rounded bg-secondary/60 px-1.5 py-px text-[10px] text-secondary-foreground sm:inline"
            >
              {t.name}
            </span>
          ))}
          <span className="shrink-0">· {timeAgo(d.lastPostedAt)}</span>
        </div>
      </div>
      <div
        className="flex shrink-0 items-center gap-1 text-xs text-muted-foreground"
        title={`${d.commentCount} 回复`}
      >
        <MessageSquare className="size-3.5" />
        {d.commentCount}
      </div>
    </div>
  )
}

export function DiscussionRow({ d }: { d: DiscussionSummary }) {
  return (
    <div className="flex gap-3 py-3.5 border-b border-border/50 last:border-b-0">
      <Link to={`/u/${d.author?.username ?? ""}`} className="shrink-0">
        <UserAvatar user={d.author} />
      </Link>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5 flex-wrap">
          {d.hasUnread && (
            <span className="size-2 rounded-full bg-primary shrink-0" title="有未读更新" />
          )}
          {d.isSticky && (
            <span className="inline-flex items-center gap-0.5 rounded bg-orange-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-orange-600 dark:text-orange-400">
              置顶
            </span>
          )}
          {d.isLocked && (
            <span className="inline-flex items-center gap-0.5 rounded bg-blue-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-blue-600 dark:text-blue-400">
              锁定
            </span>
          )}
          {d.isHidden && (
            <span className="inline-flex items-center rounded bg-red-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-red-600 dark:text-red-400">
              已删除
            </span>
          )}
          <Link
            to={`/d/${d.id}-${d.slug}`}
            className="font-semibold text-foreground hover:text-primary truncate"
          >
            {d.title}
          </Link>
        </div>
        <div className="mt-1 flex items-center gap-2 text-xs text-muted-foreground flex-wrap">
          <Link to={`/u/${d.author?.username ?? ""}`} className="hover:text-primary">
            {d.author?.username}
          </Link>
          {d.tags.map((t) => (
            <TagChip key={t.id} tag={t} />
          ))}
        </div>
      </div>
      <div className="flex flex-col items-end justify-center text-xs text-muted-foreground whitespace-nowrap">
        <span>
          <b className="text-primary text-sm">{d.commentCount}</b> 回复
        </span>
        <span>{timeAgo(d.lastPostedAt)}</span>
        {d.lastPoster && <span>最后 {d.lastPoster}</span>}
      </div>
    </div>
  )
}
