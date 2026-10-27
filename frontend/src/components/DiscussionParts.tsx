import { Link, useNavigate } from "react-router"
import { MessageSquare, Pin, Lock, Trash2 } from "lucide-react"
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

// 现代化 Reddit / Shadcn 风格讨论卡片：加粗超大标题、全宽视觉空间、清晰元信息与交互药丸
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

  return (
    <article
      role="link"
      tabIndex={0}
      onClick={open}
      onKeyDown={(e) => e.key === "Enter" && open()}
      className="group relative flex flex-col justify-between rounded-xl border border-border/70 bg-card p-4 transition-all duration-200 hover:border-primary/40 hover:shadow-xs cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring sm:p-5"
    >
      {/* 顶部元数据：版块徽标 + 作者头像/名称 + 时间 + 状态 */}
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        {mainTag && <TagChip tag={mainTag} />}
        <Link
          to={`/u/${d.author?.username ?? ""}`}
          onClick={(e) => e.stopPropagation()}
          className="flex items-center gap-1.5 font-medium text-foreground/85 hover:text-primary hover:underline"
        >
          <UserAvatar user={d.author} className="size-5 ring-1 ring-border/60" />
          <span>{d.author?.username}</span>
        </Link>
        <span>•</span>
        <time>{timeAgo(d.lastPostedAt)}</time>

        <div className="ml-auto flex items-center gap-1.5">
          {d.hasUnread && (
            <span
              className="inline-flex items-center gap-1 rounded-full bg-blue-500/15 px-2 py-0.5 text-[11px] font-semibold text-blue-600 dark:text-blue-400"
              title="有未读更新"
            >
              <span className="size-1.5 rounded-full bg-blue-600 dark:bg-blue-400" />
              未读
            </span>
          )}
          {d.isSticky && (
            <span className="inline-flex items-center gap-0.5 rounded-md bg-amber-500/15 px-1.5 py-0.5 text-[11px] font-semibold text-amber-700 dark:text-amber-400">
              <Pin className="size-3" /> 置顶
            </span>
          )}
          {d.isLocked && (
            <span className="inline-flex items-center gap-0.5 rounded-md bg-zinc-500/15 px-1.5 py-0.5 text-[11px] font-semibold text-zinc-700 dark:text-zinc-400">
              <Lock className="size-3" /> 锁定
            </span>
          )}
          {d.isHidden && (
            <span className="inline-flex items-center gap-0.5 rounded-md bg-red-500/15 px-1.5 py-0.5 text-[11px] font-semibold text-red-600 dark:text-red-400">
              <Trash2 className="size-3" /> 已删除
            </span>
          )}
        </div>
      </div>

      {/* 标题：Reddit 风格大号粗体 */}
      <h2 className="my-2.5 text-base sm:text-lg lg:text-[19px] font-bold tracking-tight text-foreground group-hover:text-primary transition-colors leading-snug">
        {d.title}
      </h2>

      {/* 次级标签 */}
      {d.tags.length > 1 && (
        <div className="mb-2 flex flex-wrap gap-1.5">
          {d.tags.slice(1).map((t) => (
            <TagChip key={t.id} tag={t} />
          ))}
        </div>
      )}

      {/* 底部交互条：回复数药丸与最后活跃人 */}
      <div className="flex items-center justify-between pt-1 text-xs text-muted-foreground">
        <span
          className="inline-flex items-center gap-1.5 rounded-full bg-secondary/80 px-3 py-1 font-semibold text-secondary-foreground transition-colors group-hover:bg-primary/10 group-hover:text-primary"
        >
          <MessageSquare className="size-3.5" />
          {d.commentCount} 条回复
        </span>

        {d.lastPoster && (
          <span className="text-[11px] text-muted-foreground">
            最后回复: {d.lastPoster}
          </span>
        )}
      </div>
    </article>
  )
}

export function DiscussionRow({ d }: { d: DiscussionSummary }) {
  return <DiscussionRowCompact d={d} />
}
