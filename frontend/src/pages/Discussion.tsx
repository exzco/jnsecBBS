import { useEffect, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link, useNavigate, useParams, useSearchParams } from "react-router"
import {
  ChevronLeft, Flag, Heart, Lock, Pencil, Pin, Tag as TagIcon, Trash2,
} from "lucide-react"
import { toast } from "sonner"
import { useSession } from "@/lib/session"
import { api, discussionUrl } from "@/lib/api"
import { markDiscussionAsReadInCache } from "@/lib/readCache"
import type { DiscussionDetail, Post } from "@/lib/types"
import { MarkdownBody } from "@/components/MarkdownBody"
import { MarkdownEditor } from "@/components/MarkdownEditor"
import { Pagination } from "@/components/Pagination"
import { PostEditor } from "@/components/PostEditor"
import { GroupBadges, TagChip, UserAvatar } from "@/components/DiscussionParts"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { timeAgo } from "@/lib/format"

export default function DiscussionPage() {
  const { idSlug = "" } = useParams()
  const id = Number(idSlug.split("-")[0]) || 0
  return (
    <div>
      <Button asChild variant="ghost" size="sm" className="-ml-2 mb-2 gap-1 text-muted-foreground hover:text-foreground">
        <Link to="/">
          <ChevronLeft className="size-4" /> 返回论坛
        </Link>
      </Button>
      <DiscussionView id={id} idSlug={idSlug} syncUrl />
    </div>
  )
}

// 首页中栏内嵌详情：保持三栏结构，返回按钮回到列表
export function InlineDiscussion({ id, onBack }: { id: number; onBack: () => void }) {
  return (
    <div>
      <Button variant="ghost" size="sm" className="-ml-2 mb-2 gap-1 text-muted-foreground" onClick={onBack}>
        <ChevronLeft className="size-4" /> 返回列表
      </Button>
      <DiscussionView id={id} />
    </div>
  )
}

function DiscussionView({ id, idSlug = "", syncUrl = false }: { id: number; idSlug?: string; syncUrl?: boolean }) {
  const [params, setParams] = useSearchParams()
  const hasPage = params.has("page")
  const requestedPage = hasPage ? Number(params.get("page")) : null
  const page = requestedPage && Number.isSafeInteger(requestedPage) && requestedPage > 0 ? requestedPage : 0
  const goPage = (p: number) => {
    setParams((current) => {
      const next = new URLSearchParams(current)
      next.set("page", String(p))
      next.delete("edit")
      return next
    })
    window.scrollTo({ top: 0, behavior: "smooth" })
    document.getElementById("discussion-top")?.scrollIntoView({ behavior: "smooth" })
  }
  const navigate = useNavigate()
  const qc = useQueryClient()

  const { data, isError, error } = useQuery({
    queryKey: ["discussion", id, page],
    queryFn: () => api<DiscussionDetail>(`/api/discussions/${id}${page > 0 ? `?page=${page}` : ""}`),
    retry: false,
  })

  // 1. 同步更新所有讨论流的已读缓存，确保返回列表时未读点即时消除
  useEffect(() => {
    if (data) {
      markDiscussionAsReadInCache(qc, id)
    }
  }, [data, id, qc])

  // 2. 首次打开帖子（未指定 page）且包含多页内容/周报时，即时渲染直达目标楼层，消除 150ms 延迟动画闪烁
  useEffect(() => {
    if (data && !hasPage) {
      const isWeekly = data.discussion.tags.some((t) => t.name.includes("周报") || t.slug === "weekly")
      if (isWeekly || data.page.page > 1) {
        const last = data.posts[data.posts.length - 1]
        if (last) {
          document.getElementById(`post-${last.id}`)?.scrollIntoView({ behavior: "instant", block: "nearest" })
        }
      }
    }
  }, [data?.discussion.id, hasPage])

  // 旧链接 /d/{id} 规范化为 /d/{id}-{slug}（仅整页路由）
  useEffect(() => {
    if (syncUrl && data && idSlug !== `${data.discussion.id}-${data.discussion.slug}` && data.discussion.slug) {
      navigate(discussionUrl(data.discussion.id, data.discussion.slug) + `?${params}`, {
        replace: true,
      })
    }
  }, [data, idSlug, navigate, params, syncUrl])

  const reply = useMutation({
    mutationFn: (content: string) =>
      api<{ postId: number; number: number; page: number }>(`/api/discussions/${id}/reply`, {
        json: { content },
      }),
    onSuccess: (r) => {
      toast.success("回复成功")
      void qc.invalidateQueries({ queryKey: ["discussion", id] })
      void qc.invalidateQueries({ queryKey: ["me"] })
      goPage(r.page)
      setTimeout(() => {
        document.getElementById(`post-${r.postId}`)?.scrollIntoView({ behavior: "smooth" })
      }, 300)
    },
    onError: (e) => toast.error((e as Error).message),
  })

  const mod = useMutation({
    mutationFn: (body: Record<string, unknown>) =>
      api(`/api/discussions/${id}/mod`, { json: body }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["discussion", id] })
      void qc.invalidateQueries({ queryKey: ["tagDiscussions"] })
      void qc.invalidateQueries({ queryKey: ["home"] })
    },
    onError: (e) => toast.error((e as Error).message),
  })

  if (isError) {
    return (
      <div className="py-20 text-center">
        <h1 className="text-xl font-bold">{(error as Error).message}</h1>
        <Button asChild variant="link">
          <Link to="/">返回首页</Link>
        </Button>
      </div>
    )
  }
  if (!data) {
    return <div className="py-20 text-center text-muted-foreground">加载中…</div>
  }

  const d = data.discussion
  const uniqueTags = Array.from(new Map(d.tags.map((t) => [t.id, t])).values())

  return (
    <div id="discussion-top">
      <div className="mb-4">
        <div className="flex flex-wrap items-center gap-2">
          {d.isHidden && (
            <span className="rounded bg-red-500/10 px-2 py-0.5 text-xs font-semibold text-red-600 dark:text-red-400">
              已删除
            </span>
          )}
          <h1 className="text-2xl font-bold">{d.title}</h1>
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
          {uniqueTags.map((t) => (
            <TagChip key={t.id} tag={t} />
          ))}
          <Link to={`/u/${d.author?.username ?? ""}`} className="flex items-center gap-1.5 hover:text-primary">
            <UserAvatar user={d.author} className="size-6" />
            {d.author?.username}
          </Link>
          <time>{timeAgo(d.createdAt)}</time>
          {d.isLocked && (
            <span className="inline-flex items-center gap-1 text-xs">
              <Lock className="size-3.5" /> 已锁定
            </span>
          )}
        </div>
      </div>

      {data.isMod && <ModBar d={d} editorTags={data.editorTags} onAction={mod.mutate} />}

      <section className="flex flex-col">
        {data.posts.map((p) => (
          <PostItem key={p.id} p={p} />
        ))}
      </section>

      <Pagination page={data.page} onPage={goPage} />

      {data.canReply ? (
        <div className="mt-2 rounded-xl bg-muted/40 p-4">
          <ReplyForm onSubmit={(v) => reply.mutate(v)} submitting={reply.isPending} />
        </div>
      ) : (
        <p className="py-6 text-center text-sm text-muted-foreground">
          {d.isLocked ? "该帖子已被锁定，无法回复。" : "登录后参与讨论。"}
        </p>
      )}
    </div>
  )
}

function ModBar({
  d, editorTags, onAction,
}: {
  d: DiscussionDetail["discussion"]
  editorTags: DiscussionDetail["editorTags"]
  onAction: (body: Record<string, unknown>) => void
}) {
  const [editing, setEditing] = useState(false)
  const [title, setTitle] = useState(d.title)
  const [tagIds, setTagIds] = useState<number[]>(() => Array.from(new Set(d.tags.map((t) => t.id))))

  useEffect(() => {
    setTitle(d.title)
    setTagIds(Array.from(new Set(d.tags.map((t) => t.id))))
  }, [d.title, d.tags])

  const uniqueEditorTags = Array.from(new Map(editorTags.map((t) => [t.id, t])).values())

  function toggleTag(tid: number) {
    setTagIds((cur) => (cur.includes(tid) ? cur.filter((x) => x !== tid) : [...cur, tid]))
  }

  return (
    <div className="mb-4 rounded-xl border border-border/70 bg-card p-3 shadow-xs">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs font-semibold text-muted-foreground mr-1">版主操作：</span>
        <Button variant="outline" size="sm" className="gap-1.5" onClick={() => onAction({ action: "toggle_sticky" })}>
          <Pin className="size-3.5" /> {d.isSticky ? "取消置顶" : "置顶"}
        </Button>
        <Button variant="outline" size="sm" className="gap-1.5" onClick={() => onAction({ action: "toggle_lock" })}>
          <Lock className="size-3.5" /> {d.isLocked ? "解锁" : "锁定"}
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="gap-1.5 text-destructive hover:bg-destructive/10 hover:text-destructive"
          onClick={() => {
            if (confirm(`确定${d.isHidden ? "恢复" : "删除"}该帖子？`)) onAction({ action: "toggle_hidden" })
          }}
        >
          <Trash2 className="size-3.5" /> {d.isHidden ? "恢复" : "删除"}
        </Button>
        <Button variant="outline" size="sm" className="gap-1.5" onClick={() => setEditing(!editing)}>
          <Pencil className="size-3.5" /> 编辑标题与标签
        </Button>
      </div>
      {editing && (
        <div className="mt-3 space-y-3 border-t border-border/60 pt-3">
          <Input value={title} onChange={(e) => setTitle(e.target.value)} maxLength={200} placeholder="帖子标题" />
          <div className="flex flex-wrap gap-2">
            {uniqueEditorTags.map((t) => (
              <label key={t.id} className="flex cursor-pointer items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs has-[:checked]:bg-primary has-[:checked]:text-primary-foreground">
                <input type="checkbox" className="hidden" checked={tagIds.includes(t.id)} onChange={() => toggleTag(t.id)} />
                <TagIcon className="size-3" /> {t.name}
              </label>
            ))}
          </div>
          <Button size="sm" onClick={() => { onAction({ action: "edit_meta", title, tagIds: Array.from(new Set(tagIds)) }); setEditing(false) }}>
            保存
          </Button>
        </div>
      )}
    </div>
  )
}

function PostItem({ p }: { p: Post }) {
  const { data: session } = useSession()
  const [params, setParams] = useSearchParams()
  const [editing, setEditing] = useState(params.get("edit") === String(p.id))
  const closeEditor = () => {
    setEditing(false)
    if (params.get("edit") === String(p.id)) {
      setParams((current) => {
        const next = new URLSearchParams(current)
        next.delete("edit")
        return next
      }, { replace: true })
    }
  }
  const qc = useQueryClient()
  const like = useMutation({
    mutationFn: () => api<{ liked: boolean; likeCount: number }>(`/api/posts/${p.id}/like`, { method: "POST" }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["discussion"] }),
    onError: (e) => toast.error((e as Error).message),
  })
  const hide = useMutation({
    mutationFn: () => api<{ hidden: boolean }>(`/api/posts/${p.id}/hide`, { method: "POST" }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["discussion"] }),
    onError: (e) => toast.error((e as Error).message),
  })

  return (
    <article
      id={`post-${p.id}`}
      className={`border-b border-border/50 px-1 py-5 last:border-b-0 ${p.hidden ? "opacity-60" : ""}`}
    >
      <header className="mb-2 flex items-center gap-2.5">
        <Link to={`/u/${p.author?.username ?? ""}`}>
          <UserAvatar user={p.author} />
        </Link>
        <div className="flex flex-col leading-tight">
          <Link to={`/u/${p.author?.username ?? ""}`} className="flex items-center gap-1.5 text-sm font-semibold hover:text-primary">
            {p.author?.username}
            {p.author && <GroupBadges user={p.author} />}
          </Link>
          <time className="text-xs text-muted-foreground">
            {timeAgo(p.createdAt)}
            {p.editedAt ? " · 已编辑" : ""}
          </time>
        </div>
        <span className="ml-auto text-xs text-muted-foreground">#{p.number}</span>
      </header>

      {editing && p.canEdit ? (
        <PostEditor post={p} onClose={closeEditor} />
      ) : p.hidden ? (
        <p className="text-sm text-muted-foreground">该回复已被隐藏。</p>
      ) : (
        <MarkdownBody html={p.html} />
      )}

      <footer className="mt-3 flex items-center gap-3 text-sm">
        {p.canEdit && !editing && (
          <button type="button" onClick={() => setEditing(true)} className="inline-flex items-center gap-1 text-muted-foreground hover:text-primary">
            <Pencil className="size-3.5" /> 编辑
          </button>
        )}
        {p.canHide && (
          <button
            className="inline-flex items-center gap-1 text-muted-foreground hover:text-red-600"
            onClick={() => hide.mutate()}
          >
            <Flag className="size-3.5" /> {p.hidden ? "恢复" : "隐藏"}
          </button>
        )}
        <button
          disabled={session?.readOnly}
          onClick={() => like.mutate()}
          className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-muted-foreground transition-colors hover:bg-rose-500/10 hover:text-rose-600 ${
            p.liked ? "bg-rose-500/10 font-semibold text-rose-600" : ""
          }`}
        >
          <Heart className={`size-4 ${p.liked ? "fill-rose-600" : ""}`} /> {p.likeCount}
        </button>
      </footer>
    </article>
  )
}

function ReplyForm({ onSubmit, submitting }: { onSubmit: (v: string) => void; submitting: boolean }) {
  const [content, setContent] = useState("")
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        if (content.trim()) {
          onSubmit(content)
          setContent("")
        }
      }}
    >
      <MarkdownEditor value={content} onChange={setContent} rows={5} placeholder="发表回复…（支持 Markdown，@用户名 可提醒对方）" />
      <div className="mt-3 flex items-center justify-between">
        <span className="text-xs text-muted-foreground">支持 Markdown</span>
        <Button type="submit" disabled={submitting || !content.trim()}>
          发表回复
        </Button>
      </div>
    </form>
  )
}
