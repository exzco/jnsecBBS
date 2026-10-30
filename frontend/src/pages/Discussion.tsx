import { useEffect, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link, useNavigate, useParams, useSearchParams } from "react-router"
import {
  ChevronLeft, Flag, Heart, Lock, Pencil, Pin, Quote, Reply, Tag as TagIcon, Trash2,
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

  const [replyContent, setReplyContent] = useState("")
  const [selectionQuote, setSelectionQuote] = useState<{
    postId: number
    postNumber: number
    author: string
    text: string
    top: number
    left: number
  } | null>(null)

  useEffect(() => {
    const updateSelection = () => {
      const selection = window.getSelection()
      if (!selection || selection.isCollapsed || !selection.rangeCount) {
        setSelectionQuote(null)
        return
      }

      const text = selection.toString().trim()
      if (!text || text.length < 2) {
        setSelectionQuote(null)
        return
      }

      const range = selection.getRangeAt(0)
      const container = range.commonAncestorContainer
      const element = (container.nodeType === Node.ELEMENT_NODE ? container : container.parentElement) as HTMLElement | null
      if (!element) {
        setSelectionQuote(null)
        return
      }

      // 忽略输入框/编辑器内部的划线
      if (element.closest("textarea, input, [contenteditable='true']")) {
        setSelectionQuote(null)
        return
      }

      const postArticle = element.closest("article[data-post-id]") as HTMLElement | null
      if (!postArticle) {
        setSelectionQuote(null)
        return
      }

      // 确保选区在帖子正文内
      const insideBody = element.closest("[data-post-body]") || postArticle.querySelector("[data-post-body]")?.contains(container)
      if (!insideBody) {
        setSelectionQuote(null)
        return
      }

      const postId = Number(postArticle.dataset.postId)
      const postNumber = Number(postArticle.dataset.postNumber)
      const author = postArticle.dataset.postAuthor || ""
      const rect = range.getBoundingClientRect()
      if (rect.width === 0 && rect.height === 0) {
        setSelectionQuote(null)
        return
      }

      let top = rect.top - 42
      if (top < 60) {
        top = rect.bottom + 8
      }
      const left = Math.max(30, Math.min(window.innerWidth - 60, rect.left + rect.width / 2))

      setSelectionQuote({
        postId,
        postNumber,
        author,
        text,
        top,
        left,
      })
    }

    const onMouseUp = () => {
      setTimeout(updateSelection, 10)
    }

    const onKeyUp = (e: KeyboardEvent) => {
      if (["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Shift"].includes(e.key)) {
        setTimeout(updateSelection, 10)
      }
    }

    const onSelectionChange = () => {
      const sel = window.getSelection()
      if (!sel || sel.isCollapsed) {
        setSelectionQuote(null)
      }
    }

    const onScrollOrResize = () => {
      setSelectionQuote(null)
    }

    document.addEventListener("mouseup", onMouseUp)
    document.addEventListener("touchend", onMouseUp)
    document.addEventListener("keyup", onKeyUp)
    document.addEventListener("selectionchange", onSelectionChange)
    window.addEventListener("scroll", onScrollOrResize, { passive: true })
    window.addEventListener("resize", onScrollOrResize, { passive: true })

    return () => {
      document.removeEventListener("mouseup", onMouseUp)
      document.removeEventListener("touchend", onMouseUp)
      document.removeEventListener("keyup", onKeyUp)
      document.removeEventListener("selectionchange", onSelectionChange)
      window.removeEventListener("scroll", onScrollOrResize)
      window.removeEventListener("resize", onScrollOrResize)
    }
  }, [])

  const reply = useMutation({
    mutationFn: (content: string) =>
      api<{ postId: number; number: number; page: number }>(`/api/discussions/${id}/reply`, {
        json: { content },
      }),
    onSuccess: (r) => {
      toast.success("回复成功")
      setReplyContent("")
      void qc.invalidateQueries({ queryKey: ["discussion", id] })
      void qc.invalidateQueries({ queryKey: ["me"] })
      goPage(r.page)
      setTimeout(() => {
        document.getElementById(`post-${r.postId}`)?.scrollIntoView({ behavior: "smooth" })
      }, 300)
    },
    onError: (e) => toast.error((e as Error).message),
  })

  const handleReplyTo = (p: Post, quoteText?: string) => {
    let textToQuote = quoteText
    if (!textToQuote) {
      const sel = window.getSelection()
      if (sel && !sel.isCollapsed) {
        const anc = sel.anchorNode?.parentElement
        if (anc?.closest(`article[data-post-id="${p.id}"]`)) {
          textToQuote = sel.toString().trim()
        }
      }
    }

    const author = p.author?.username ?? "用户"
    if (textToQuote && textToQuote.trim()) {
      const clean = textToQuote.trim()
      const lines = clean.split("\n").map((l) => `> ${l.trimEnd()}`).join("\n")
      const quote = `> @"${author}"#p${p.number}\n${lines}\n\n`
      setReplyContent((cur) => (cur.trim() ? `${cur.trimEnd()}\n\n${quote}` : quote))
      window.getSelection()?.removeAllRanges()
      setSelectionQuote(null)
      toast.success("已添加划线引用")
    } else {
      const quote = `@${author} #${p.number} `
      setReplyContent((cur) => (cur ? `${cur}\n${quote}` : quote))
    }

    document.getElementById("reply-form")?.scrollIntoView({ behavior: "smooth" })
    setTimeout(() => {
      const ta = document.getElementById("reply-textarea") as HTMLTextAreaElement | null
      if (ta) {
        ta.focus()
        const len = ta.value.length
        ta.setSelectionRange(len, len)
      }
    }, 100)
  }

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
          <PostItem key={p.id} p={p} canReply={data.canReply} onReplyTo={handleReplyTo} />
        ))}
      </section>

      <Pagination page={data.page} onPage={goPage} />

      {data.canReply ? (
        <div id="reply-form" className="mt-2 rounded-xl bg-muted/40 p-4">
          <ReplyForm
            value={replyContent}
            onChange={setReplyContent}
            onSubmit={(v) => reply.mutate(v)}
            submitting={reply.isPending}
          />
        </div>
      ) : (
        <p className="py-6 text-center text-sm text-muted-foreground">
          {d.isLocked ? "该帖子已被锁定，无法回复。" : "登录后参与讨论。"}
        </p>
      )}

      {selectionQuote && (
        <div
          className="fixed z-50 animate-in fade-in zoom-in-95 duration-100"
          style={{
            top: `${selectionQuote.top}px`,
            left: `${selectionQuote.left}px`,
            transform: "translateX(-50%)",
          }}
        >
          <button
            type="button"
            onMouseDown={(e) => {
              e.preventDefault()
              e.stopPropagation()
            }}
            onClick={() => {
              if (!data.canReply) {
                toast.error(d.isLocked ? "该帖子已被锁定，无法回复" : "请登录后参与讨论")
                return
              }
              const post = data.posts.find((x) => x.id === selectionQuote.postId)
              if (post) {
                handleReplyTo(post, selectionQuote.text)
              } else {
                const author = selectionQuote.author || "用户"
                const clean = selectionQuote.text.trim()
                const lines = clean.split("\n").map((l) => `> ${l.trimEnd()}`).join("\n")
                const quote = `> @"${author}"#p${selectionQuote.postNumber}\n${lines}\n\n`
                setReplyContent((cur) => (cur.trim() ? `${cur.trimEnd()}\n\n${quote}` : quote))
                window.getSelection()?.removeAllRanges()
                setSelectionQuote(null)
                document.getElementById("reply-form")?.scrollIntoView({ behavior: "smooth" })
                toast.success("已添加划线引用")
                setTimeout(() => {
                  const ta = document.getElementById("reply-textarea") as HTMLTextAreaElement | null
                  ta?.focus()
                }, 100)
              }
            }}
            className="flex items-center gap-1.5 rounded-full border border-border/80 bg-popover/95 px-3 py-1.5 text-xs font-semibold text-popover-foreground shadow-lg backdrop-blur-md transition hover:bg-accent hover:text-accent-foreground active:scale-95 cursor-pointer ring-1 ring-black/5 dark:ring-white/10"
          >
            <Quote className="size-3.5 text-primary" />
            <span>引用回复</span>
          </button>
        </div>
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

function PostItem({
  p,
  canReply,
  onReplyTo,
}: {
  p: Post
  canReply?: boolean
  onReplyTo?: (p: Post, quoteText?: string) => void
}) {
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
      data-post-id={p.id}
      data-post-number={p.number}
      data-post-author={p.author?.username ?? ""}
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
        <div data-post-body>
          <MarkdownBody html={p.html} />
        </div>
      )}

      <footer className="mt-3 flex items-center gap-3 text-sm">
        {canReply && (
          <button
            type="button"
            onClick={() => onReplyTo?.(p)}
            className="inline-flex items-center gap-1 text-muted-foreground hover:text-primary"
            title="回复或引用选中内容"
          >
            <Reply className="size-3.5" /> 回复
          </button>
        )}
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

function ReplyForm({
  value,
  onChange,
  onSubmit,
  submitting,
}: {
  value: string
  onChange: (v: string) => void
  onSubmit: (v: string) => void
  submitting: boolean
}) {
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        if (value.trim()) {
          onSubmit(value)
          onChange("")
        }
      }}
    >
      <MarkdownEditor
        id="reply-textarea"
        value={value}
        onChange={onChange}
        rows={5}
        placeholder="发表回复…（支持划线快捷引用、Markdown 与 LaTeX，@用户名 可提醒对方）"
      />
      <div className="mt-3 flex items-center justify-between">
        <span className="text-xs text-muted-foreground">支持划线快捷引用、Markdown 与 LaTeX ($E=mc^2$)</span>
        <Button type="submit" disabled={submitting || !value.trim()}>
          发表回复
        </Button>
      </div>
    </form>
  )
}
