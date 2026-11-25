import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link } from "react-router"
import { AtSign, ChevronLeft, Heart, Reply } from "lucide-react"
import { toast } from "sonner"
import { useSession } from "@/lib/session"
import { api, discussionUrl } from "@/lib/api"
import type { NotificationItem } from "@/lib/types"
import { UserAvatar } from "@/components/DiscussionParts"
import { Button } from "@/components/ui/button"
import { timeAgo } from "@/lib/format"

const icons = { reply: Reply, mention: AtSign, like: Heart }

export default function NotificationsPage() {
  const { data: session } = useSession()
  const qc = useQueryClient()
  const { data } = useQuery({
    queryKey: ["notifications"],
    queryFn: () => api<{ items: NotificationItem[]; unread: number }>("/api/me/notifications"),
    refetchOnWindowFocus: true,
  })

  const readAll = useMutation({
    mutationFn: () => api("/api/me/notifications/read", { method: "POST" }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["notifications"] })
      void qc.invalidateQueries({ queryKey: ["me"] })
      void qc.invalidateQueries({ queryKey: ["unread"] })
      toast.success("已全部标记为已读")
    },
  })

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-border/70 bg-card p-4 shadow-xs">
        <div className="flex items-center gap-2">
          <Button asChild variant="ghost" size="sm" className="-ml-2 gap-1 text-muted-foreground hover:text-foreground">
            <Link to="/">
              <ChevronLeft className="size-4" /> 返回论坛
            </Link>
          </Button>
          <h1 className="text-xl font-bold tracking-tight text-foreground">通知</h1>
          {(data?.unread ?? 0) > 0 && (
            <span className="rounded-full bg-red-500/10 px-2 py-0.5 text-xs font-semibold text-red-600">
              {data?.unread} 条未读
            </span>
          )}
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => readAll.mutate()}
          disabled={readAll.isPending || session?.readOnly || (data?.items.length ?? 0) === 0}
        >
          全部标记已读
        </Button>
      </div>

      <div className="divide-y divide-border/50 overflow-hidden rounded-xl border border-border/70 bg-card shadow-xs">
        {(data?.items.length ?? 0) === 0 ? (
          <p className="p-12 text-center text-sm text-muted-foreground">暂无新通知。</p>
        ) : (
          data!.items.map((n) => {
            const Icon = icons[n.type] ?? Reply
            const url = n.discussion
              ? `${discussionUrl(n.discussion.id, n.discussion.slug)}#post-${n.postId}`
              : "#"
            return (
              <Link
                key={n.id}
                to={url}
                className={`flex items-center gap-3.5 px-4 py-3.5 transition-colors hover:bg-muted/50 ${
                  n.read ? "" : "bg-primary/5 font-medium"
                }`}
              >
                <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary">
                  <Icon className="size-4" />
                </span>
                <UserAvatar user={n.sender} className="size-8" />
                <div className="min-w-0 flex-1 text-sm">
                  <p className="text-foreground">
                    <span className="font-semibold">{n.sender?.username}</span>{" "}
                    {n.type === "reply" && "回复了你的讨论"}
                    {n.type === "mention" && "在讨论中提到了你"}
                    {n.type === "like" && "赞了你的发言"}
                  </p>
                  {n.discussion && (
                    <p className="mt-0.5 truncate text-xs text-primary/80">
                      {n.discussion.title}
                    </p>
                  )}
                </div>
                <time className="shrink-0 text-xs text-muted-foreground">{timeAgo(n.createdAt)}</time>
              </Link>
            )
          })
        )}
      </div>
    </div>
  )
}
