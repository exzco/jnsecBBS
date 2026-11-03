import { useEffect, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link, useNavigate, useParams, useSearchParams } from "react-router"
import {
  ArrowUpToLine, ChevronLeft, Eye, Flag, Heart, Loader2, Lock, Pencil, Pin, Quote, Reply, Tag as TagIcon, Trash2, Users,
} from "lucide-react"
import { toast } from "sonner"
import { useSession } from "@/lib/session"
import { api, discussionUrl } from "@/lib/api"
import { markDiscussionAsReadInCache } from "@/lib/readCache"
import type { DiscussionDetail, Post, User } from "@/lib/types"
import { MarkdownBody } from "@/components/MarkdownBody"
import { MarkdownEditor } from "@/components/MarkdownEditor"
import { PostEditor } from "@/components/PostEditor"
import { GroupBadges, TagChip, UserAvatar } from "@/components/DiscussionParts"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { timeAgo } from "@/lib/format"

export default function DiscussionPage() {
  const { idSlug = "" } = useParams()
  const id = Number(idSlug.split("-")[0]) || 0
  const navigate = useNavigate()
  const handleBack = () => {
    if (window.history.length > 1) {
      navigate(-1)
    } else {
      navigate("/")
    }
  }
  return <DiscussionView id={id} idSlug={idSlug} syncUrl onBack={handleBack} />
}

// 首页中栏内嵌详情：保持三栏结构，吸顶标题栏集成返回按钮
export function InlineDiscussion({ id, onBack }: { id: number; onBack: () => void }) {
  return <DiscussionView id={id} onBack={onBack} />
}

function DiscussionView({
  id,
  idSlug = "",
  syncUrl = false,
  onBack,
}: {
  id: number
  idSlug?: string
  syncUrl?: boolean
  onBack?: () => void
}) {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const qc = useQueryClient()

  const { data, isError, error } = useQuery({
    queryKey: ["discussion", id],
    queryFn: () => api<DiscussionDetail>(`/api/discussions/${id}?all=1`),
    retry: false,
  })

  // 1. 同步更新所有讨论流的已读缓存，确保返回列表时未读点即时消除
  useEffect(() => {
    if (data) {
      markDiscussionAsReadInCache(qc, id)
    }
  }, [data, id, qc])

  // 2. 监听用户滚动，记忆当前帖子的阅读楼层与位置（持久化到 localStorage）
  useEffect(() => {
    if (!data || data.posts.length === 0) return
    const scroller = document.getElementById("forum-content") || document.documentElement
    let timer: ReturnType<typeof setTimeout> | null = null
    const storageKey = `bbs_disc_read_pos_${id}`

    const handleScroll = () => {
      if (timer) clearTimeout(timer)
      timer = setTimeout(() => {
        const top = scroller.scrollTop
        let visiblePostId: number | undefined
        let visiblePostNumber: number | undefined

        // 计算当前视口中最靠顶部的帖子楼层
        for (const p of data.posts) {
          const el = document.getElementById(`post-${p.id}`)
          if (el) {
            const rect = el.getBoundingClientRect()
            if (rect.bottom > 80) {
              visiblePostId = p.id
              visiblePostNumber = p.number
              break
            }
          }
        }

        try {
          localStorage.setItem(
            storageKey,
            JSON.stringify({
              scrollTop: top,
              postId: visiblePostId,
              postNumber: visiblePostNumber,
              updatedAt: Date.now(),
            }),
          )
        } catch {
          // ignore storage error
        }
      }, 150)
    }

    scroller.addEventListener("scroll", handleScroll, { passive: true })
    return () => {
      if (timer) clearTimeout(timer)
      scroller.removeEventListener("scroll", handleScroll)
    }
  }, [data, id])

  // 3. 打开讨论定位：第一次进入周报直达最底部最新楼层；后续再次进入恢复到之前看到的楼层
  useEffect(() => {
    if (!data || data.posts.length === 0) return

    const isWeekly =
      data.discussion.tags.some((t) => t.name.includes("周报") || t.slug === "weekly") ||
      data.discussion.title.includes("周报")

    const scroller = document.getElementById("forum-content") || document.documentElement
    const storageKey = `bbs_disc_read_pos_${id}`
    const storedStr = localStorage.getItem(storageKey)

    const doScroll = () => {
      // 1. 如果已有阅读记录（非第一次打开），精准恢复到之前看到的楼层
      if (storedStr) {
        try {
          const parsed = JSON.parse(storedStr)
          if (parsed.postId) {
            const targetEl = document.getElementById(`post-${parsed.postId}`)
            if (targetEl) {
              targetEl.scrollIntoView({ behavior: "instant", block: "start" })
              return
            }
          }
          if (typeof parsed.scrollTop === "number" && parsed.scrollTop > 0) {
            scroller.scrollTo({ top: parsed.scrollTop, behavior: "instant" })
            return
          }
        } catch {
          // ignore json error
        }
      }

      // 2. 第一次打开周报：直达最底部最新周报楼层
      if (isWeekly) {
        const last = data.posts[data.posts.length - 1]
        const lastEl = last ? document.getElementById(`post-${last.id}`) : null
        if (lastEl) {
          lastEl.scrollIntoView({ behavior: "instant", block: "end" })
        } else {
          scroller.scrollTo({ top: scroller.scrollHeight, behavior: "instant" })
        }
        // 标记已阅读过最新周报，后续再次进入将保持阅读进度
        if (last) {
          try {
            localStorage.setItem(
              storageKey,
              JSON.stringify({
                scrollTop: scroller.scrollTop,
                postId: last.id,
                postNumber: last.number,
                updatedAt: Date.now(),
              }),
            )
          } catch {
            // ignore
          }
        }
      }
    }

    let raf2: number | null = null
    const raf1 = requestAnimationFrame(() => {
      raf2 = requestAnimationFrame(() => {
        doScroll()
      })
    })

    const t1 = setTimeout(doScroll, 80)
    const t2 = setTimeout(doScroll, 260)

    return () => {
      cancelAnimationFrame(raf1)
      if (raf2) cancelAnimationFrame(raf2)
      clearTimeout(t1)
      clearTimeout(t2)
    }
  }, [data?.discussion.id, id])

  // 旧链接 /d/{id} 规范化为 /d/{id}-{slug}（仅整页路由）
  useEffect(() => {
    if (syncUrl && data && idSlug !== `${data.discussion.id}-${data.discussion.slug}` && data.discussion.slug) {
      navigate(discussionUrl(data.discussion.id, data.discussion.slug) + (params.toString() ? `?${params}` : ""), {
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
      setTimeout(() => {
        document.getElementById(`post-${r.postId}`)?.scrollIntoView({ behavior: "smooth" })
      }, 300)
    },
    onError: (e) => toast.error((e as Error).message),
  })

  const handleReplyTo = (p: Post, quoteText?: string, quoteWholePost?: boolean) => {
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

    if (!textToQuote && quoteWholePost) {
      textToQuote = p.content.trim()
      if (textToQuote.length > 280) {
        textToQuote = textToQuote.slice(0, 280).trim() + "..."
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
      toast.success("已添加引用回复")
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

  const { data: session } = useSession()
  const [isEditingTitle, setIsEditingTitle] = useState(false)
  const [titleInput, setTitleInput] = useState("")

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
  const isAuthor = Boolean(session?.user && d.author && session.user.id === d.author.id)
  const canEditTitle = Boolean(data.isMod || isAuthor)
  const isWeeklyDiscussion = Boolean(
    data.isWeekly ||
    d.tags.some((t) => t.name.includes("周报") || t.slug === "weekly") ||
    d.title.includes("周报")
  )

  const [isFromStart, setIsFromStart] = useState(false)
  const [startPosts, setStartPosts] = useState<Post[]>([])
  const [startPage, setStartPage] = useState(1)
  const [startTotalPages, setStartTotalPages] = useState(1)
  const [isLoadingStart, setIsLoadingStart] = useState(false)
  const [isLoadingMore, setIsLoadingMore] = useState(false)

  // 当讨论切换时重置从头加载状态
  useEffect(() => {
    setIsFromStart(false)
    setStartPosts([])
    setStartPage(1)
    setStartTotalPages(1)
  }, [id])

  const handleSwitchToStart = async () => {
    if (isLoadingStart) return
    setIsLoadingStart(true)
    try {
      const res = await api<DiscussionDetail>(`/api/discussions/${id}?from_start=1&page=1&per_page=20`)
      setStartPosts(res.posts)
      setStartPage(res.page.page)
      setStartTotalPages(res.page.pages)
      setIsFromStart(true)
      const scroller = document.getElementById("forum-content") || document.documentElement
      scroller.scrollTo({ top: 0, behavior: "smooth" })
      toast.success("已置顶，正在浏览最早周报，往下滑动逐步加载")
    } catch (e) {
      toast.error("加载数据失败: " + (e as Error).message)
    } finally {
      setIsLoadingStart(false)
    }
  }

  const handleLoadMore = async (nextPage: number) => {
    if (isLoadingMore || nextPage > startTotalPages) return
    setIsLoadingMore(true)
    try {
      const res = await api<DiscussionDetail>(`/api/discussions/${id}?from_start=1&page=${nextPage}&per_page=20`)
      setStartPosts((prev) => {
        const existingIds = new Set(prev.map((p) => p.id))
        const newPosts = res.posts.filter((p) => !existingIds.has(p.id))
        return [...prev, ...newPosts]
      })
      setStartPage(res.page.page)
      setStartTotalPages(res.page.pages)
    } catch (e) {
      toast.error("加载更多数据失败: " + (e as Error).message)
    } finally {
      setIsLoadingMore(false)
    }
  }

  useEffect(() => {
    if (!isFromStart) return
    const scroller = document.getElementById("forum-content") || document.documentElement

    const onScroll = () => {
      if (isLoadingMore || isLoadingStart) return
      if (startPage >= startTotalPages) return

      const scrollHeight = scroller.scrollHeight
      const scrollTop = scroller.scrollTop || document.documentElement.scrollTop
      const clientHeight = scroller.clientHeight || window.innerHeight

      if (scrollHeight - (scrollTop + clientHeight) < 300) {
        handleLoadMore(startPage + 1)
      }
    }

    scroller.addEventListener("scroll", onScroll, { passive: true })
    return () => scroller.removeEventListener("scroll", onScroll)
  }, [isFromStart, startPage, startTotalPages, isLoadingMore, isLoadingStart, id])

  const handlePostHidden = (postId: number, hidden: boolean) => {
    if (isFromStart) {
      setStartPosts((prev) => prev.map((item) => (item.id === postId ? { ...item, hidden } : item)))
    }
  }

  const handlePostLiked = (postId: number, liked: boolean, likeCount: number, likers?: User[]) => {
    if (isFromStart) {
      setStartPosts((prev) =>
        prev.map((item) => (item.id === postId ? { ...item, liked, likeCount, likers: likers || item.likers } : item))
      )
    }
  }

  const displayPosts = isFromStart ? startPosts : data.posts

  const handleSaveTitle = () => {
    const clean = titleInput.trim()
    if (!clean || clean === d.title) {
      setIsEditingTitle(false)
      return
    }
    mod.mutate(
      { action: "edit_meta", title: clean, tagIds: d.tags.map((t) => t.id) },
      {
        onSuccess: () => {
          setIsEditingTitle(false)
          toast.success("讨论标题已更新")
        },
      }
    )
  }

  const uniqueTags = Array.from(new Map(d.tags.map((t) => [t.id, t])).values())

  return (
    <div id="discussion-top">
      {/* 吸顶标题栏：随着往下翻自动固定吸附在最上方，方便随时查看与修改名称 */}
      <header className="sticky top-0 z-20 -mx-1 sm:-mx-2 px-1 sm:px-2 pt-1 pb-2.5 mb-3 bg-background/95 backdrop-blur-md border-b border-border/50 shadow-xs transition-colors">
        {isEditingTitle ? (
          <div className="rounded-xl border border-primary/40 bg-card p-3 shadow-xs animate-in fade-in-50">
            <div className="mb-2 text-xs font-semibold text-primary">修改周报 / 讨论标题</div>
            <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
              <Input
                value={titleInput}
                onChange={(e) => setTitleInput(e.target.value)}
                maxLength={200}
                placeholder="请输入新的讨论标题"
                className="flex-1 font-medium"
                autoFocus
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault()
                    handleSaveTitle()
                  } else if (e.key === "Escape") {
                    setIsEditingTitle(false)
                  }
                }}
              />
              <div className="flex items-center gap-2">
                <Button size="sm" disabled={!titleInput.trim() || mod.isPending} onClick={handleSaveTitle}>
                  {mod.isPending ? "保存中…" : "保存"}
                </Button>
                <Button size="sm" variant="outline" onClick={() => setIsEditingTitle(false)}>
                  取消
                </Button>
              </div>
            </div>
          </div>
        ) : (
          <div className="flex items-center gap-2">
            {onBack && (
              <Button
                variant="ghost"
                size="icon"
                className="size-7 shrink-0 text-muted-foreground hover:bg-muted hover:text-foreground -ml-1 cursor-pointer"
                title="返回列表"
                aria-label="返回列表"
                onClick={onBack}
              >
                <ChevronLeft className="size-4" />
              </Button>
            )}
            {d.isHidden && (
              <span className="rounded bg-red-500/10 px-2 py-0.5 text-xs font-semibold text-red-600 dark:text-red-400 shrink-0">
                已删除
              </span>
            )}
            <h1 className="text-lg sm:text-2xl font-bold tracking-tight text-foreground truncate flex-1 min-w-0">
              {d.title}
            </h1>
            {canEditTitle && (
              <Button
                variant="ghost"
                size="icon"
                className="size-7 shrink-0 rounded-lg text-muted-foreground hover:bg-muted hover:text-primary transition-colors cursor-pointer"
                title="修改标题"
                aria-label="修改标题"
                onClick={() => {
                  setTitleInput(d.title)
                  setIsEditingTitle(true)
                }}
              >
                <Pencil className="size-3.5" />
              </Button>
            )}
          </div>
        )}
        <div className="mt-1.5 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
          {uniqueTags.map((t) => (
            <TagChip key={t.id} tag={t} />
          ))}
          <Link to={`/u/${d.author?.username ?? ""}`} className="flex items-center gap-1.5 hover:text-primary">
            <UserAvatar user={d.author} className="size-5" />
            {d.author?.username}
          </Link>
          <time>{timeAgo(d.createdAt)}</time>
          <span className="inline-flex items-center gap-1 text-xs" title={`浏览量: ${d.viewCount ?? 0} 次`}>
            <Eye className="size-3" /> {d.viewCount ?? 0} 次
          </span>
          {d.isLocked && (
            <span className="inline-flex items-center gap-1 text-xs">
              <Lock className="size-3" /> 已锁定
            </span>
          )}
        </div>
      </header>

      {data.isMod && <ModBar d={d} editorTags={data.editorTags} onAction={mod.mutate} />}

      {isWeeklyDiscussion && !isFromStart && (
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2 rounded-xl border border-primary/20 bg-primary/5 px-3.5 py-2.5 text-xs text-primary shadow-2xs">
          <div className="flex items-center gap-1.5 font-medium">
            <span>
              已为您加载近 {data.windowDays || 30} 天周报内容（共 {displayPosts.length} 篇）
            </span>
          </div>
          <Button
            size="sm"
            variant="outline"
            className="h-7 gap-1.5 border-primary/30 bg-background/90 hover:bg-primary hover:text-primary-foreground text-xs font-semibold cursor-pointer shadow-2xs transition-colors"
            onClick={handleSwitchToStart}
            disabled={isLoadingStart}
          >
            <ArrowUpToLine className="size-3.5" />
            {isLoadingStart ? "加载中…" : "一键置顶"}
          </Button>
        </div>
      )}

      {isWeeklyDiscussion && isFromStart && (
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2 rounded-xl border border-border/70 bg-muted/40 px-3.5 py-2.5 text-xs text-muted-foreground shadow-2xs">
          <div className="flex items-center gap-1.5 font-medium text-foreground">
            <ArrowUpToLine className="size-3.5 text-primary" />
            <span>已置顶浏览最早周报（往下滑动逐步加载，已加载 {startPosts.length} 篇）</span>
          </div>
          <Button
            size="sm"
            variant="ghost"
            className="h-7 gap-1 text-xs text-primary hover:bg-primary/10 cursor-pointer"
            onClick={() => {
              setIsFromStart(false)
              const scroller = document.getElementById("forum-content") || document.documentElement
              scroller.scrollTo({ top: 0, behavior: "smooth" })
            }}
          >
            返回最新周报
          </Button>
        </div>
      )}

      <section className="flex flex-col">
        {displayPosts.map((p) => (
          <PostItem
            key={p.id}
            p={p}
            canReply={data.canReply}
            onReplyTo={handleReplyTo}
            onHiddenChange={handlePostHidden}
            onLikedChange={handlePostLiked}
          />
        ))}
        {isFromStart && (
          <div className="py-4 text-center text-xs text-muted-foreground">
            {isLoadingMore ? (
              <span className="inline-flex items-center gap-1.5 text-primary">
                <Loader2 className="size-3.5 animate-spin" />
                正在加载后续周报…
              </span>
            ) : startPage < startTotalPages ? (
              <button
                type="button"
                onClick={() => handleLoadMore(startPage + 1)}
                className="text-primary hover:underline cursor-pointer"
              >
                下滑或点击加载更多 (当前已加载至第 {startPage}/{startTotalPages} 页)
              </button>
            ) : (
              <span>已加载全部周报内容（共 {startPosts.length} 篇）</span>
            )}
          </div>
        )}
      </section>

      {data.viewers && data.viewers.length > 0 && (
        <div className="mt-4 mb-2 rounded-xl border border-border/70 bg-card/60 p-3.5 shadow-xs">
          <div className="mb-2.5 flex items-center justify-between">
            <div className="flex items-center gap-1.5 text-xs font-semibold text-muted-foreground">
              <Users className="size-3.5 text-primary" />
              <span>最近浏览者</span>
              <span className="rounded-full bg-muted px-1.5 py-0.2 text-[11px] font-normal text-muted-foreground">
                {data.viewers.length} 人
              </span>
            </div>
            <span className="text-xs text-muted-foreground">
              累计浏览 {d.viewCount ?? 0} 次
            </span>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {data.viewers.map((viewer) => (
              <Link
                key={viewer.user.id}
                to={`/u/${viewer.user.username}`}
                title={`${viewer.user.username} · ${timeAgo(viewer.visitedAt)}`}
                className="group flex items-center gap-1.5 rounded-full border border-border/60 bg-background/80 py-1 pl-1 pr-2.5 text-xs transition hover:border-primary/50 hover:bg-muted"
              >
                <UserAvatar user={viewer.user} className="size-5" />
                <span className="font-medium text-foreground/90 group-hover:text-primary">
                  {viewer.user.username}
                </span>
                <span className="text-[11px] text-muted-foreground/75">
                  {timeAgo(viewer.visitedAt)}
                </span>
              </Link>
            ))}
          </div>
        </div>
      )}

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

      {isWeeklyDiscussion && (
        <button
          type="button"
          onClick={
            isFromStart
              ? () => {
                  const scroller = document.getElementById("forum-content") || document.documentElement
                  scroller.scrollTo({ top: 0, behavior: "smooth" })
                }
              : handleSwitchToStart
          }
          disabled={isLoadingStart}
          className="fixed bottom-6 right-6 z-30 flex items-center gap-1.5 rounded-full border border-primary/30 bg-background/95 px-3.5 py-2 text-xs font-semibold text-primary shadow-lg backdrop-blur-sm transition-all hover:bg-primary hover:text-primary-foreground active:scale-95 cursor-pointer disabled:opacity-50"
          title={isFromStart ? "回到顶部" : "一键置顶：查询最开始的数据并向下滑动逐步加载"}
        >
          {isLoadingStart ? (
            <Loader2 className="size-3.5 animate-spin" />
          ) : (
            <ArrowUpToLine className="size-3.5" />
          )}
          <span>{isFromStart ? "回到顶部" : "一键置顶"}</span>
        </button>
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
  onHiddenChange,
  onLikedChange,
}: {
  p: Post
  canReply?: boolean
  onReplyTo?: (p: Post, quoteText?: string, quoteWholePost?: boolean) => void
  onHiddenChange?: (postId: number, hidden: boolean) => void
  onLikedChange?: (postId: number, liked: boolean, likeCount: number, likers?: User[]) => void
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
    mutationFn: () =>
      api<{ liked: boolean; likeCount: number; likers: User[] }>(`/api/posts/${p.id}/like`, { method: "POST" }),
    onMutate: async () => {
      await qc.cancelQueries({ queryKey: ["discussion"] })
      const previousData = qc.getQueriesData<DiscussionDetail>({ queryKey: ["discussion"] })

      const currentUser = session?.user
      const nextLiked = !p.liked
      const nextCount = nextLiked ? p.likeCount + 1 : Math.max(0, p.likeCount - 1)
      const currentLikers = p.likers || []
      let nextLikers = currentLikers
      if (currentUser) {
        if (nextLiked) {
          nextLikers = [currentUser, ...currentLikers.filter((u) => u.id !== currentUser.id)]
        } else {
          nextLikers = currentLikers.filter((u) => u.id !== currentUser.id)
        }
      }

      qc.setQueriesData<DiscussionDetail>({ queryKey: ["discussion"] }, (old) => {
        if (!old) return old
        return {
          ...old,
          posts: old.posts.map((item) =>
            item.id === p.id
              ? {
                  ...item,
                  liked: nextLiked,
                  likeCount: nextCount,
                  likers: nextLikers,
                }
              : item
          ),
        }
      })

      return { previousData }
    },
    onSuccess: (res) => {
      onLikedChange?.(p.id, res.liked, res.likeCount, res.likers)
      qc.setQueriesData<DiscussionDetail>({ queryKey: ["discussion"] }, (old) => {
        if (!old) return old
        return {
          ...old,
          posts: old.posts.map((item) =>
            item.id === p.id
              ? {
                  ...item,
                  liked: res.liked,
                  likeCount: res.likeCount,
                  likers: res.likers,
                }
              : item
          ),
        }
      })
    },
    onError: (e, _vars, context) => {
      if (context?.previousData) {
        for (const [key, val] of context.previousData) {
          qc.setQueryData(key, val)
        }
      }
      toast.error((e as Error).message)
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ["discussion"] })
    },
  })
  const hide = useMutation({
    mutationFn: () => api<{ hidden: boolean }>(`/api/posts/${p.id}/hide`, { method: "POST" }),
    onSuccess: (res) => {
      onHiddenChange?.(p.id, res.hidden)
      void qc.invalidateQueries({ queryKey: ["discussion"] })
    },
    onError: (e) => toast.error((e as Error).message),
  })

  const likers = p.likers || []
  const totalLikes = Math.max(p.likeCount || 0, likers.length)
  const shownLikers = likers.slice(0, 8)
  const remainingCount = totalLikes - shownLikers.length

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
          <MarkdownBody content={p.content} html={p.html} />
        </div>
      )}

      <footer className="mt-3 flex flex-wrap items-center gap-3 text-sm">
        {canReply && (
          <>
            <button
              type="button"
              onClick={() => onReplyTo?.(p, undefined, true)}
              className="inline-flex items-center gap-1 text-muted-foreground hover:text-primary transition-colors cursor-pointer"
              title="引用此楼层内容并回复"
            >
              <Quote className="size-3.5" /> 引用
            </button>
            <button
              type="button"
              onClick={() => onReplyTo?.(p)}
              className="inline-flex items-center gap-1 text-muted-foreground hover:text-primary transition-colors cursor-pointer"
              title="提及该楼层作者"
            >
              <Reply className="size-3.5" /> 回复
            </button>
          </>
        )}
        {p.canEdit && !editing && (
          <button type="button" onClick={() => setEditing(true)} className="inline-flex items-center gap-1 text-muted-foreground hover:text-primary">
            <Pencil className="size-3.5" /> 编辑
          </button>
        )}
        {p.canHide && (
          <button
            className="inline-flex items-center gap-1 text-muted-foreground hover:text-red-600 cursor-pointer"
            onClick={() => {
              const actionName = p.hidden ? "恢复" : "隐藏"
              if (window.confirm(`确定${actionName}该帖子内容？`)) {
                hide.mutate()
              }
            }}
          >
            <Flag className="size-3.5" /> {p.hidden ? "恢复" : "隐藏"}
          </button>
        )}
        <div className="inline-flex items-center gap-1.5">
          <button
            disabled={session?.readOnly || like.isPending}
            onClick={() => {
              if (!session?.user) {
                toast.error("请登录后点赞")
                return
              }
              like.mutate()
            }}
            className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-muted-foreground transition-colors hover:bg-rose-500/10 hover:text-rose-600 disabled:opacity-50 disabled:pointer-events-none cursor-pointer ${
              p.liked ? "bg-rose-500/10 font-semibold text-rose-600" : ""
            }`}
            title={p.liked ? "取消赞" : "点赞"}
          >
            <Heart className={`size-4 ${p.liked ? "fill-rose-600 text-rose-600" : ""}`} /> {p.likeCount}
          </button>

          {shownLikers.length > 0 && (
            <div className="flex items-center -space-x-1.5 pl-0.5" aria-label="点赞用户">
              {shownLikers.map((u) => (
                <div key={u.id} className="group/liker relative inline-flex hover:z-30">
                  <Link
                    to={`/u/${encodeURIComponent(u.username)}`}
                    className="relative inline-block rounded-full transition-transform duration-150 hover:scale-125 focus:outline-none"
                  >
                    <UserAvatar
                      user={u}
                      className="size-5.5 border-2 border-background ring-1 ring-border/40 shadow-xs"
                    />
                  </Link>
                  {/* 人物浮动提示气泡 */}
                  <div className="pointer-events-none absolute -top-8 left-1/2 -translate-x-1/2 z-40 hidden whitespace-nowrap rounded-md bg-popover px-2 py-0.5 text-[11px] font-medium text-popover-foreground shadow-md ring-1 ring-border group-hover/liker:block">
                    {u.username}
                    <div className="absolute left-1/2 -bottom-1 -translate-x-1/2 size-0 border-x-4 border-t-4 border-x-transparent border-t-popover" />
                  </div>
                </div>
              ))}
              {remainingCount > 0 && (
                <div className="group/liker relative inline-flex hover:z-30">
                  <span
                    className="relative flex size-5.5 items-center justify-center rounded-full border-2 border-background bg-muted text-[10px] font-semibold text-muted-foreground shadow-xs ring-1 ring-border/40 cursor-default"
                  >
                    +{remainingCount}
                  </span>
                  <div className="pointer-events-none absolute -top-8 left-1/2 -translate-x-1/2 z-40 hidden whitespace-nowrap rounded-md bg-popover px-2 py-0.5 text-[11px] font-medium text-popover-foreground shadow-md ring-1 ring-border group-hover/liker:block">
                    等共 {totalLikes} 人点赞
                    <div className="absolute left-1/2 -bottom-1 -translate-x-1/2 size-0 border-x-4 border-t-4 border-x-transparent border-t-popover" />
                  </div>
                </div>
              )}
            </div>
          )}
        </div>
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
