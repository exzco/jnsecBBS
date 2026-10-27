import { useEffect, useMemo } from "react"
import { useInfiniteQuery, useQuery } from "@tanstack/react-query"
import { Link, useSearchParams } from "react-router"
import { Plus, Search } from "lucide-react"
import { api } from "@/lib/api"
import { useSession } from "@/lib/session"
import { timeAgo } from "@/lib/format"
import { useHomeBoard } from "@/lib/stores"
import type { HomeCard, RecentData } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { DiscussionRowCompact } from "@/components/DiscussionParts"
import { InlineDiscussion } from "@/pages/Discussion"
import { cn } from "cn"

export default function HomePage() {
  const { data: session, isLoading: sessionLoading } = useSession()
  const siteName = session?.siteName ?? "JNSEC BBS"
  const user = session?.user ?? null
  const [board, setBoard] = useHomeBoard()
  // ?d=<id>：中栏内嵌文章详情；清空回到列表
  const [searchParams, setSearchParams] = useSearchParams()
  const selectedId = Number(searchParams.get("d")) || 0
  const openDiscussion = (id: number) => setSearchParams({ d: String(id) })
  const selectBoard = (slug: string) => {
    setBoard(slug)
    setSearchParams({})
  }

  const { data: homeData, isLoading: homeLoading } = useQuery({
    queryKey: ["home"],
    queryFn: () => api<{ cards: HomeCard[] }>("/api/home"),
    enabled: !!user,
  })
  const boards = useMemo(() => (homeData?.cards ?? []).map((c) => c.tag), [homeData])
  // 大板块与子板块都可直接选中（旧 /t/{slug} 链接与标签芯片跳转过来）
  const allSlugs = useMemo(() => {
    const s = new Set<string>()
    for (const c of homeData?.cards ?? []) {
      s.add(c.tag.slug)
      for (const ch of c.children) s.add(ch.slug)
    }
    return s
  }, [homeData])
  // 持久化/传入的板块可能已被删除：找不到时回落“全部”
  const active = board !== "all" && allSlugs.has(board) ? board : "all"
  const activeCard = homeData?.cards.find(
    (c) => c.tag.slug === active || c.children.some((x) => x.slug === active),
  )
  const activeTag = activeCard
    ? activeCard.tag.slug === active ? activeCard.tag : activeCard.children.find((x) => x.slug === active)
    : undefined
  // 左栏高亮：子板块选中时高亮其父板块
  const activeTop = activeCard?.tag.slug
  const isAll = active === "all"

  // 旧链接 /t/{slug} 重定向为 /?board=slug：数据就绪后选中并清掉该参数
  useEffect(() => {
    if (homeLoading || !searchParams.has("board")) return
    const b = searchParams.get("board") ?? ""
    if (allSlugs.has(b)) setBoard(b)
    setSearchParams({}, { replace: true })
  }, [homeLoading, searchParams, setSearchParams, allSlugs, setBoard])

  // 主栏：全部 → /api/recent；选中板块 → 该板块文章列表（含子板块）
  const main = useInfiniteQuery({
    queryKey: isAll ? ["recent", "feed"] : ["tag-discussions", active],
    queryFn: ({ pageParam }) =>
      api<RecentData>(`${isAll ? "/api/recent" : `/api/tags/${active}/discussions`}?page=${pageParam}`),
    initialPageParam: 1,
    getNextPageParam: (last) => (last.page.page < last.page.pages ? last.page.page + 1 : undefined),
    enabled: !!user,
  })
  const mainItems = main.data?.pages.flatMap((p) => p.items) ?? []
  const mainTotal = main.data?.pages[0]?.page.total

  // 右侧最新栏：全站最新，服务端 sidebar=1 自动过滤周报
  const sidebar = useQuery({
    queryKey: ["recent", "sidebar"],
    queryFn: () => api<RecentData>("/api/recent?page=1&sidebar=1"),
    enabled: !!user,
  })

  return (
    <div>
      {/* 未登录：欢迎横幅（文案可在管理后台设置），版块内容不可见 */}
      {!user && !sessionLoading && (
        <div className="mb-6 rounded-xl bg-gradient-to-r from-[#005661] to-[#00838f] px-6 py-10 text-center text-white shadow-sm">
          <h1 className="text-2xl font-bold">欢迎来到 {siteName}</h1>
          <p className="mx-auto mt-2 max-w-xl text-sm leading-relaxed text-white/85">
            {session?.welcomeText || "内部论坛，所有内容请登录后查看，请勿在其他渠道发布本论坛的内容。"}
          </p>
          <div className="mt-5 flex justify-center gap-3">
            <Button asChild className="bg-white text-[#006064] hover:bg-white/90">
              <Link to="/login">登录</Link>
            </Button>
            {!session?.readOnly && <Button
              asChild
              variant="outline"
              className="border-white/70 bg-transparent text-white hover:bg-white/15 hover:text-white"
            >
              <Link to="/register">注册</Link>
            </Button>}
          </div>
        </div>
      )}

      {user && (
        <div className="grid gap-5 min-[880px]:grid-cols-[160px_minmax(0,1fr)_240px]">
          {/* 移动端：板块横滑条 */}
          <div className="-mx-4 flex gap-2 overflow-x-auto px-4 pb-1 min-[880px]:hidden">
            <BoardChip slug="all" name="全部" active={isAll} onClick={selectBoard} />
            {boards.map((t) => (
              <BoardChip key={t.id} slug={t.slug} name={t.name} color={t.color} active={activeTop === t.slug} onClick={selectBoard} />
            ))}
          </div>

          {/* 左栏：板块导航 */}
          <aside className="hidden flex-col gap-4 self-start min-[880px]:flex min-[880px]:sticky min-[880px]:top-20">
            {!session?.readOnly && (
              <div className="flex gap-2">
                <Button asChild className="flex-1 gap-1.5">
                  <Link to="/post">
                    <Plus className="size-4" /> 发新帖
                  </Link>
                </Button>
                <Button asChild variant="outline" size="icon" aria-label="搜索">
                  <Link to="/search">
                    <Search className="size-4" />
                  </Link>
                </Button>
              </div>
            )}
            <nav className="overflow-hidden rounded-xl border bg-card">
              <h2 className="border-b px-4 py-2.5 text-sm font-semibold">版块</h2>
              <ul className="p-2">
                {homeLoading ? (
                  Array.from({ length: 6 }).map((_, i) => <Skeleton key={i} className="mx-2 my-2 h-7 rounded-md" />)
                ) : (
                  <>
                    <li>
                      <BoardLink slug="all" name="全部" active={isAll} onClick={selectBoard} />
                    </li>
                    {boards.map((t) => (
                      <li key={t.id}>
                        <BoardLink slug={t.slug} name={t.name} color={t.color} count={t.discussionsCount} active={activeTop === t.slug} onClick={selectBoard} />
                      </li>
                    ))}
                  </>
                )}
              </ul>
            </nav>
          </aside>

          {/* 中栏：内嵌文章详情 或 当前板块文章列表 */}
          <div className="min-w-0">
            {selectedId > 0 ? (
              <InlineDiscussion key={selectedId} id={selectedId} onBack={() => setSearchParams({})} />
            ) : (
              <>
                <div className="mb-3 flex items-baseline justify-between gap-2">
                  <h1 className="text-lg font-bold">
                    {isAll ? "全部讨论" : activeTag?.name}
                    {mainTotal !== undefined && (
                      <span className="ml-2 text-sm font-normal text-muted-foreground">{mainTotal}</span>
                    )}
                  </h1>
                </div>
                {main.isLoading ? (
                  <div className="space-y-3">
                    {Array.from({ length: 8 }).map((_, i) => (
                      <Skeleton key={i} className="h-12 rounded-lg" />
                    ))}
                  </div>
                ) : (
                  <div className="overflow-hidden rounded-xl border bg-card">
                    {mainItems.map((d) => (
                      <DiscussionRowCompact key={d.id} d={d} onOpen={openDiscussion} />
                    ))}
                    {mainItems.length === 0 && (
                      <p className="py-16 text-center text-sm text-muted-foreground">该版块暂无讨论。</p>
                    )}
                  </div>
                )}
                {main.hasNextPage && (
                  <div className="mt-4 text-center">
                    <Button variant="outline" onClick={() => void main.fetchNextPage()} disabled={main.isFetchingNextPage}>
                      {main.isFetchingNextPage ? "加载中…" : "加载更多"}
                    </Button>
                  </div>
                )}
              </>
            )}
          </div>

          {/* 右栏：最新文章（不含周报） */}
          <aside className="min-w-0 self-start min-[880px]:sticky min-[880px]:top-20">
            <div className="overflow-hidden rounded-xl border bg-card">
              <h2 className="border-b px-4 py-2.5 text-sm font-semibold">最新文章</h2>
              {sidebar.isLoading ? (
                <div className="space-y-3 p-4">
                  {Array.from({ length: 6 }).map((_, i) => (
                    <Skeleton key={i} className="h-8 rounded-md" />
                  ))}
                </div>
              ) : (
                <ul className="p-2">
                  {(sidebar.data?.items ?? []).slice(0, 10).map((d) => (
                    <li key={d.id}>
                      <Link
                        to={`/d/${d.id}-${d.slug}`}
                        onClick={(e) => {
                          e.preventDefault()
                          openDiscussion(d.id)
                        }}
                        className="group block rounded-md px-2 py-2 hover:bg-muted"
                      >
                        <p className="truncate text-sm group-hover:text-primary flex items-center gap-1.5">
                          {d.hasUnread && <span className="size-1.5 rounded-full bg-primary shrink-0" title="未读更新" />}
                          <span className="truncate">{d.title}</span>
                        </p>
                        <p className="mt-0.5 flex items-center gap-2 text-xs text-muted-foreground">
                          <span>{d.author?.username}</span>
                          <span>· {timeAgo(d.lastPostedAt)}</span>
                          <span>· {d.commentCount} 回复</span>
                        </p>
                      </Link>
                    </li>
                  ))}
                  {(sidebar.data?.items ?? []).length === 0 && (
                    <li className="py-8 text-center text-sm text-muted-foreground">暂无更新。</li>
                  )}
                </ul>
              )}
            </div>
          </aside>
        </div>
      )}
    </div>
  )
}

function BoardLink({
  slug,
  name,
  color,
  count,
  active,
  onClick,
}: {
  slug: string
  name: string
  color?: string
  count?: number
  active: boolean
  onClick: (slug: string) => void
}) {
  return (
    <button
      onClick={() => onClick(slug)}
      className={cn(
        "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm transition-colors hover:bg-muted",
        active && "bg-secondary font-medium text-primary",
      )}
    >
      {color ? (
        <span className="size-2 shrink-0 rounded-full" style={{ background: color || "#1e88e5" }} />
      ) : (
        <span className="flex size-2 shrink-0 items-center justify-center">
          <span className={cn("size-1.5 rounded-full bg-muted-foreground/60", active && "bg-primary")} />
        </span>
      )}
      <span className="min-w-0 flex-1 truncate text-left">{name}</span>
      {count !== undefined && <span className="shrink-0 text-xs text-muted-foreground">{count}</span>}
    </button>
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
        active ? "border-primary/40 bg-secondary font-medium text-primary" : "text-muted-foreground hover:bg-muted",
      )}
    >
      {color && <span className="size-1.5 rounded-full" style={{ background: color }} />}
      {name}
    </button>
  )
}
