import { useEffect, useMemo, useRef, useState } from "react"
import { Link, Outlet, useLocation, useNavigate, useSearchParams } from "react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { ChevronDown, Compass, Layers, MessageSquare, Plus, Search, Sparkles } from "lucide-react"
import { api } from "@/lib/api"
import { useSession } from "@/lib/session"
import { useHomeBoard } from "@/lib/stores"
import { markDiscussionAsReadInCache } from "@/lib/readCache"
import { timeAgo } from "@/lib/format"
import type { HomeCard, RecentData } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "cn"

interface WeeklyDiscussionItem {
  id: number
  title: string
  replyCount: number
  hasUnread?: boolean
  author?: { username: string }
}

function WeeklyDiscussionsList({
  tagSlug,
  openDiscussion,
  currentDiscussionId,
}: {
  tagSlug: string
  openDiscussion: (id: number) => void
  currentDiscussionId: number | null
}) {
  const qc = useQueryClient()
  const [filter, setFilter] = useState("")
  const [readIds, setReadIds] = useState<Set<number>>(() => new Set())

  const { data, isLoading } = useQuery({
    queryKey: ["weekly-discussions-all", tagSlug],
    queryFn: () =>
      api<{ items: WeeklyDiscussionItem[] }>(
        `/api/tags/${encodeURIComponent(tagSlug)}/discussions?all=1`,
      ),
    staleTime: 60_000,
  })

  if (isLoading) {
    return (
      <div className="ml-5 space-y-1.5 border-l border-border/50 pl-2 py-1">
        <div className="h-4 w-3/4 rounded bg-muted/60 animate-pulse" />
        <div className="h-4 w-2/3 rounded bg-muted/60 animate-pulse" />
        <div className="h-4 w-4/5 rounded bg-muted/60 animate-pulse" />
      </div>
    )
  }

  const items = data?.items ?? []
  const filtered = filter.trim()
    ? items.filter((x) => x.title.toLowerCase().includes(filter.toLowerCase()))
    : items

  return (
    <div className="ml-5 space-y-1 border-l border-border/50 pl-1.5 py-0.5 animate-in fade-in-50 duration-150 pr-1">
      {items.length > 8 && (
        <div className="relative mb-1">
          <Search className="pointer-events-none absolute left-2 top-1/2 size-3 -translate-y-1/2 text-muted-foreground" />
          <input
            type="text"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="搜索周报/作者…"
            className="w-full rounded-md border border-border/60 bg-muted/30 py-1 pl-6 pr-2 text-[11px] text-foreground placeholder:text-muted-foreground/60 focus:border-primary/50 focus:outline-none"
          />
        </div>
      )}
      <div className="max-h-72 overflow-y-auto space-y-0.5 pr-0.5">
        {filtered.length === 0 ? (
          <div className="py-1 text-[11px] text-muted-foreground">未找到匹配周报</div>
        ) : (
          filtered.map((item) => {
            const isActive = currentDiscussionId === item.id
            const isUnread = Boolean(item.hasUnread && !isActive && !readIds.has(item.id))
            return (
              <button
                key={item.id}
                type="button"
                onClick={() => {
                  setReadIds((prev) => new Set(prev).add(item.id))
                  markDiscussionAsReadInCache(qc, item.id)
                  openDiscussion(item.id)
                }}
                title={item.title}
                className={cn(
                  "group flex w-full items-center gap-1.5 rounded-md px-2 py-1.5 text-xs transition-colors hover:bg-muted/60 text-left cursor-pointer",
                  isActive
                    ? "bg-secondary font-semibold text-primary"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                {isUnread && (
                  <span className="size-1.5 shrink-0 rounded-full bg-blue-500 animate-in fade-in" />
                )}
                <span className="flex-1 truncate">{item.title}</span>
                {item.replyCount > 0 && (
                  <span className="text-[10px] text-muted-foreground/80 shrink-0">
                    {item.replyCount}
                  </span>
                )}
              </button>
            )
          })
        )}
      </div>
    </div>
  )
}

export function ForumLayout() {
  const qc = useQueryClient()
  const { data: session, isLoading: sessionLoading } = useSession()
  const siteName = session?.siteName ?? "JNSEC BBS"
  const user = session?.user ?? null
  const [board, setBoard] = useHomeBoard()
  const [expandedSlugs, setExpandedSlugs] = useState<Set<string>>(() => new Set())
  const contentRef = useRef<HTMLDivElement>(null)

  const location = useLocation()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()

  const { data: homeData, isLoading: homeLoading } = useQuery({
    queryKey: ["home"],
    queryFn: () => api<{ cards: HomeCard[] }>("/api/home"),
    enabled: !!user,
  })

  const cards = homeData?.cards ?? []
  const boards = useMemo(() => cards.map((c) => c.tag), [cards])

  const allSlugs = useMemo(() => {
    const s = new Set<string>()
    for (const c of cards) {
      s.add(c.tag.slug)
      for (const ch of c.children) s.add(ch.slug)
    }
    return s
  }, [cards])

  const isHome = location.pathname === "/"
  const activeParam = searchParams.get("board")
  const active = isHome
    ? activeParam && allSlugs.has(activeParam)
      ? activeParam
      : board !== "all" && allSlugs.has(board)
        ? board
        : "all"
    : ""
  const isAll = isHome && active === "all"

  const activeCard = cards.find(
    (c) => c.tag.slug === active || c.children.some((x) => x.slug === active),
  )
  const activeTop = activeCard?.tag.slug

  // 页面级路由变化时，中栏滚动条置顶（页面内讨论展开/返回由各页面精准记忆滚动位置）
  const prevPathRef = useRef(location.pathname)
  useEffect(() => {
    if (prevPathRef.current !== location.pathname) {
      prevPathRef.current = location.pathname
      contentRef.current?.scrollTo({ top: 0, behavior: "instant" })
    }
  }, [location.pathname])

  // 当处于具体子版块时，自动展开对应父版块
  useEffect(() => {
    if (activeCard && activeCard.children.some((ch) => ch.slug === active)) {
      setExpandedSlugs((prev) => {
        if (prev.has(activeCard.tag.slug)) return prev
        const next = new Set(prev)
        next.add(activeCard.tag.slug)
        return next
      })
    }
  }, [active, activeCard])

  const selectBoard = (slug: string) => {
    setBoard(slug)
    if (location.pathname !== "/") {
      navigate(slug === "all" ? "/" : `/?board=${encodeURIComponent(slug)}`)
    } else {
      if (slug === "all") {
        setSearchParams({})
      } else {
        setSearchParams({ board: slug })
      }
    }
  }

  const openDiscussion = (id: number) => {
    markDiscussionAsReadInCache(qc, id)
    if (location.pathname !== "/") {
      navigate(`/?d=${id}`)
    } else {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev)
        next.set("d", String(id))
        return next
      })
    }
  }

  // 右侧最新栏：全站最新动态，服务端自动过滤周报
  const sidebar = useQuery({
    queryKey: ["recent", "sidebar"],
    queryFn: () => api<RecentData>("/api/recent?page=1&sidebar=1"),
    enabled: !!user,
  })

  // 未登录欢迎横幅
  if (!user && !sessionLoading) {
    return (
      <div className="h-full min-h-0 overflow-y-auto py-8">
        <div className="mx-auto max-w-4xl rounded-2xl bg-gradient-to-r from-[#005661] to-[#00838f] px-8 py-12 text-center text-white shadow-md">
          <h1 className="text-3xl sm:text-4xl font-extrabold tracking-tight">欢迎来到 {siteName}</h1>
          <p className="mx-auto mt-3 max-w-2xl text-base leading-relaxed text-white/90">
            {session?.welcomeText ||
              "内部安全论坛与技术知识库，所有内容请登录后查看，请勿在其他渠道发布本论坛的内容。"}
          </p>
          <div className="mt-6 flex justify-center gap-4">
            <Button asChild size="lg" className="bg-white font-semibold text-[#006064] hover:bg-white/90 shadow-sm">
              <Link to="/login" state={{ from: "/" }}>
                登录账号
              </Link>
            </Button>
            {!session?.readOnly && (
              <Button
                asChild
                size="lg"
                variant="outline"
                className="border-white/70 bg-transparent font-semibold text-white hover:bg-white/15 hover:text-white"
              >
                <Link to="/register" state={{ from: "/" }}>
                  注册加入
                </Link>
              </Button>
            )}
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0 w-full flex-col overflow-hidden">
      {/* 移动端顶部横向版块切换条（仅在主页显示） */}
      {isHome && (
        <div className="-mx-1 mb-2 flex shrink-0 gap-2 overflow-x-auto px-1 pb-1 lg:hidden">
          <BoardChip slug="all" name="版块总览" active={isAll} onClick={selectBoard} />
          {boards.map((t) => (
            <BoardChip
              key={t.id}
              slug={t.slug}
              name={t.name}
              color={t.color}
              active={activeTop === t.slug}
              onClick={selectBoard}
            />
          ))}
        </div>
      )}

      {/* 全宽三栏响应式网格：
         左侧：版块分类导航（固定视口不滚动）
         中间：内容主体模块（独立垂直滚动区，个人资料/通知/主页/详情均在此展示）
         右侧：全站最新动态帖子（固定视口不滚动）
      */}
      <div className="grid h-full min-h-0 flex-1 grid-cols-1 items-stretch gap-6 lg:grid-cols-[260px_minmax(0,1fr)] xl:grid-cols-[270px_minmax(0,1fr)_320px] 2xl:grid-cols-[290px_minmax(0,1fr)_340px] overflow-hidden">
        {/* ==================== 1. 左栏：版块分类 ==================== */}
        <aside className="hidden flex-col gap-3 lg:flex lg:h-full lg:min-h-0 pb-1">
          {!session?.readOnly && (
            <Button asChild size="lg" className="w-full shrink-0 gap-2 font-bold shadow-sm">
              <Link to="/post">
                <Plus className="size-5" /> 发布新讨论
              </Link>
            </Button>
          )}

          {/* 版块分类树 */}
          <div className="flex-1 min-h-0 overflow-y-auto rounded-xl border border-border/70 bg-card p-3 shadow-xs">
            <div className="mb-2 flex items-center justify-between px-2 text-xs font-bold uppercase tracking-wider text-muted-foreground">
              <span className="flex items-center gap-1.5">
                <Layers className="size-3.5" /> 版块分类
              </span>
              <span className="text-[11px] font-normal">{boards.length} 个版块</span>
            </div>

            {homeLoading ? (
              <div className="space-y-2 p-1">
                {Array.from({ length: 6 }).map((_, i) => (
                  <Skeleton key={i} className="h-9 rounded-lg" />
                ))}
              </div>
            ) : (
              <div className="space-y-1">
                {/* 置顶总览入口 */}
                <button
                  type="button"
                  onClick={() => selectBoard("all")}
                  className={cn(
                    "flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm font-semibold transition-colors",
                    isAll
                      ? "bg-secondary text-primary"
                      : "text-foreground/80 hover:bg-muted/60",
                  )}
                >
                  <Compass className="size-4 shrink-0 text-primary" />
                  <span className="flex-1 text-left">版块总览</span>
                </button>

                {/* 各大版块及其子版块列表 */}
                {cards.map((c) => {
                  const rawColor = c.tag.color?.trim()
                  const color = rawColor && /^#[0-9a-fA-F]{3,8}$/.test(rawColor) ? rawColor : "#00838f"
                  const isParentActive = isHome && active === c.tag.slug
                  const hasChildren = Boolean(c.children && c.children.length > 0)
                  const isWeekly = c.tag.slug === "weekly" || c.tag.name.includes("周报")
                  const canExpand = hasChildren || isWeekly
                  const isExpanded = expandedSlugs.has(c.tag.slug)
                  const currentDiscussionId = Number(searchParams.get("d")) || null

                  return (
                    <div key={c.tag.id} className="space-y-0.5">
                      <div className="flex items-center gap-1">
                        {/* 大版块按钮 */}
                        <button
                          type="button"
                          onClick={() => {
                            selectBoard(c.tag.slug)
                            if (isWeekly && !isExpanded) {
                              setExpandedSlugs((prev) => new Set(prev).add(c.tag.slug))
                            }
                          }}
                          className={cn(
                            "flex min-w-0 flex-1 items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm font-medium transition-colors hover:bg-muted/60",
                            isParentActive
                              ? "bg-secondary font-semibold text-primary"
                              : "text-foreground/80 hover:text-foreground",
                          )}
                        >
                          <span
                            className="size-2.5 shrink-0 rounded-full"
                            style={{ backgroundColor: color }}
                          />
                          <span className="flex-1 truncate text-left">{c.tag.name}</span>
                          {c.tag.discussionsCount !== undefined && (
                            <span className="rounded-full bg-muted/60 px-2 py-0.5 text-[11px] font-normal text-muted-foreground">
                              {c.tag.discussionsCount}
                            </span>
                          )}
                        </button>

                        {/* V 展开/收起按钮：支持子版块展开与周报帖子列表下拉 */}
                        {canExpand && (
                          <button
                            type="button"
                            title={
                              isExpanded
                                ? `收起 ${c.tag.name} ${isWeekly ? "帖子" : "子版块"}`
                                : `展开 ${c.tag.name} ${isWeekly ? "帖子" : "子版块"}`
                            }
                            aria-label={
                              isExpanded
                                ? `收起 ${c.tag.name} ${isWeekly ? "帖子" : "子版块"}`
                                : `展开 ${c.tag.name} ${isWeekly ? "帖子" : "子版块"}`
                            }
                            onClick={(e) => {
                              e.stopPropagation()
                              setExpandedSlugs((prev) => {
                                const next = new Set(prev)
                                if (next.has(c.tag.slug)) {
                                  next.delete(c.tag.slug)
                                } else {
                                  next.add(c.tag.slug)
                                }
                                return next
                              })
                            }}
                            className={cn(
                              "flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-all hover:bg-muted hover:text-foreground",
                              isExpanded && "text-foreground bg-muted/60",
                            )}
                          >
                            <ChevronDown
                              className={cn(
                                "size-3.5 transition-transform duration-200",
                                !isExpanded && "-rotate-90",
                              )}
                            />
                          </button>
                        )}
                      </div>

                      {/* 子板块：仅在 hasChildren 且 isExpanded 时展开展示 */}
                      {hasChildren && isExpanded && (
                        <div className="ml-5 space-y-0.5 border-l border-border/50 pl-2 py-0.5 animate-in fade-in-50 duration-150">
                          {c.children.map((ch) => {
                            const chColor = ch.color?.trim() || color
                            const isChildActive = isHome && active === ch.slug
                            return (
                              <button
                                key={ch.id}
                                type="button"
                                onClick={() => selectBoard(ch.slug)}
                                className={cn(
                                  "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-xs transition-colors hover:bg-muted/60",
                                  isChildActive
                                    ? "bg-secondary font-semibold text-primary"
                                    : "text-muted-foreground hover:text-foreground",
                                )}
                              >
                                <span
                                  className="size-1.5 shrink-0 rounded-full"
                                  style={{ backgroundColor: chColor }}
                                />
                                <span className="flex-1 truncate text-left">{ch.name}</span>
                                {ch.discussionsCount !== undefined && (
                                  <span className="text-[10px] text-muted-foreground">
                                    {ch.discussionsCount}
                                  </span>
                                )}
                              </button>
                            )
                          })}
                        </div>
                      )}

                      {/* 周报帖子列表：仅在 isWeekly 且 isExpanded 时展开展示 191 个周报帖子 */}
                      {isWeekly && isExpanded && (
                        <WeeklyDiscussionsList
                          tagSlug={c.tag.slug}
                          openDiscussion={openDiscussion}
                          currentDiscussionId={currentDiscussionId}
                        />
                      )}
                    </div>
                  )
                })}
              </div>
            )}
          </div>
        </aside>

        {/* ==================== 2. 中栏：内容展示部分（独立垂直滚动区） ==================== */}
        <section
          id="forum-content"
          ref={contentRef}
          aria-label="内容板块"
          className="min-w-0 flex-1 h-full min-h-0 overflow-y-auto pr-1 sm:pr-2 pb-24 sm:pb-8 [scrollbar-gutter:stable] overscroll-contain"
        >
          <Outlet />
        </section>

        {/* ==================== 3. 右栏：最右侧是最新的帖子（固定视口不随主内容滚动） ==================== */}
        <aside className="hidden flex-col gap-4 xl:flex xl:h-full xl:min-h-0 xl:overflow-y-auto xl:pr-1 pb-2">
          {/* 近期最新帖子 Widget */}
          <div className="overflow-hidden rounded-xl border border-border/70 bg-card shadow-xs">
            <div className="flex items-center justify-between border-b border-border/60 px-4 py-3">
              <h2 className="flex items-center gap-1.5 text-xs font-bold uppercase tracking-wider text-muted-foreground">
                <Sparkles className="size-3.5 text-primary" /> 最新帖子
              </h2>
              <span className="text-[11px] text-muted-foreground">全站动态</span>
            </div>

            {sidebar.isLoading ? (
              <div className="space-y-3 p-3">
                {Array.from({ length: 5 }).map((_, i) => (
                  <Skeleton key={i} className="h-12 rounded-lg" />
                ))}
              </div>
            ) : (
              <div className="divide-y divide-border/40 p-1">
                {(sidebar.data?.items ?? []).slice(0, 8).map((d) => (
                  <article key={d.id}>
                    <Link
                      to={`/d/${d.id}-${d.slug}`}
                      onClick={(e) => {
                        e.preventDefault()
                        openDiscussion(d.id)
                      }}
                      className="group block rounded-lg p-2.5 transition-colors hover:bg-muted/60"
                    >
                      <p className="flex items-center gap-2 truncate text-sm font-semibold text-foreground group-hover:text-primary leading-snug">
                        {d.hasUnread && (
                          <span
                            className="size-2 rounded-full bg-blue-600 ring-2 ring-blue-500/20 shrink-0"
                            title="未读更新"
                          />
                        )}
                        <span className="truncate">{d.title}</span>
                      </p>
                      <div className="mt-1 flex items-center justify-between text-xs text-muted-foreground">
                        <span className="truncate font-medium text-foreground/75">
                          {d.author?.username}
                        </span>
                        <span className="flex items-center gap-1">
                          <MessageSquare className="size-3" />
                          {d.commentCount} · {timeAgo(d.lastPostedAt)}
                        </span>
                      </div>
                    </Link>
                  </article>
                ))}

                {(sidebar.data?.items ?? []).length === 0 && (
                  <p className="py-8 text-center text-xs text-muted-foreground">
                    暂无最新文章。
                  </p>
                )}
              </div>
            )}
          </div>

          {/* 论坛信息卡片 */}
          <div className="rounded-xl border border-border/70 bg-card p-4 text-xs text-muted-foreground shadow-xs">
            <h3 className="font-semibold text-foreground mb-1">关于 {siteName}</h3>
            <p className="leading-relaxed">
              jnsec 2026 | web re pwn 
            </p>
            <div className="mt-3 pt-3 border-t border-border/50 flex justify-between text-[11px]">
              <span>JNSEC BBS © 2026</span>
            </div>
          </div>
        </aside>
      </div>
    </div>
  )
}

function BoardChip({
  slug,
  name,
  color,
  active,
  onClick,
}: {
  slug: string
  name: string
  color?: string
  active: boolean
  onClick: (slug: string) => void
}) {
  return (
    <button
      onClick={() => onClick(slug)}
      className={cn(
        "flex shrink-0 items-center gap-1.5 rounded-full border px-3 py-1 text-xs transition-colors",
        active
          ? "border-primary/40 bg-secondary font-semibold text-primary"
          : "text-muted-foreground hover:bg-muted",
      )}
    >
      {color && <span className="size-2 rounded-full" style={{ backgroundColor: color }} />}
      {name}
    </button>
  )
}
