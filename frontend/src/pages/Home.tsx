import { useEffect, useMemo, useRef, useState } from "react"
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query"
import { useSearchParams } from "react-router"
import { Compass, ChevronDown, ChevronUp, Pin } from "lucide-react"
import { api } from "@/lib/api"
import { useSession } from "@/lib/session"
import { timeAgo } from "@/lib/format"
import { useHomeBoard } from "@/lib/stores"
import { markDiscussionAsReadInCache } from "@/lib/readCache"
import type { HomeCard, RecentData } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { DiscussionRowCompact, TagChip } from "@/components/DiscussionParts"
import { InlineDiscussion } from "@/pages/Discussion"

// 跨组件保留每个板块的浏览滚动深度（内存级单例）
const boardScrollMap = new Map<string, number>()

export default function HomePage() {
  const qc = useQueryClient()
  const { data: session } = useSession()
  const user = session?.user ?? null
  const [board, setBoard] = useHomeBoard()
  const [expandSticky, setExpandSticky] = useState(false)

  // ?d=<id>：中栏内嵌文章详情；清空回到列表
  const [searchParams, setSearchParams] = useSearchParams()
  const selectedId = Number(searchParams.get("d")) || 0

  const selectBoard = (slug: string) => {
    setBoard(slug)
    if (slug === "all") {
      setSearchParams({})
    } else {
      setSearchParams({ board: slug })
    }
  }

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

  const activeParam = searchParams.get("board")
  const active = activeParam && allSlugs.has(activeParam)
    ? activeParam
    : board !== "all" && allSlugs.has(board)
      ? board
      : "all"

  const activeCard = cards.find(
    (c) => c.tag.slug === active || c.children.some((x) => x.slug === active),
  )
  const activeTag = activeCard
    ? activeCard.tag.slug === active
      ? activeCard.tag
      : activeCard.children.find((x) => x.slug === active)
    : undefined
  const isAll = active === "all"

  const openDiscussion = (id: number) => {
    markDiscussionAsReadInCache(qc, id)
    const container = document.getElementById("forum-content")
    if (container) {
      boardScrollMap.set(active, container.scrollTop)
    }
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      next.set("d", String(id))
      return next
    })
  }

  const closeDiscussion = () => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      next.delete("d")
      return next
    })
  }

  // 监听当前展开讨论的变化，实现无缝滚动记忆恢复
  const prevSelectedId = useRef(selectedId)
  useEffect(() => {
    const container = document.getElementById("forum-content")
    if (!container) return

    if (selectedId > 0 && prevSelectedId.current === 0) {
      // 打开详情时，中栏滚动置顶自上而下阅读
      container.scrollTo({ top: 0, behavior: "instant" })
    } else if (selectedId === 0 && prevSelectedId.current > 0) {
      // 从详情返回列表时，恢复先前所在板块的具体位置
      const savedY = boardScrollMap.get(active) ?? 0
      requestAnimationFrame(() => {
        container.scrollTo({ top: savedY, behavior: "instant" })
      })
    }
    prevSelectedId.current = selectedId
  }, [selectedId, active])

  // 实时记录当前活跃板块的列表滚动位置
  useEffect(() => {
    const container = document.getElementById("forum-content")
    if (!container) return

    const handleScroll = () => {
      if (selectedId === 0) {
        boardScrollMap.set(active, container.scrollTop)
      }
    }
    container.addEventListener("scroll", handleScroll, { passive: true })
    return () => container.removeEventListener("scroll", handleScroll)
  }, [active, selectedId])

  // 挂载时若无展开讨论且有历史滚动记忆，恢复之
  useEffect(() => {
    if (selectedId === 0) {
      const container = document.getElementById("forum-content")
      const savedY = boardScrollMap.get(active)
      if (container && savedY !== undefined && savedY > 0) {
        requestAnimationFrame(() => {
          container.scrollTo({ top: savedY, behavior: "instant" })
        })
      }
    }
  }, [])

  // 仅在选中具体版块时拉取该版块的帖子列表
  const main = useInfiniteQuery({
    queryKey: ["tag-discussions", active],
    queryFn: ({ pageParam }) =>
      api<RecentData>(`/api/tags/${active}/discussions?page=${pageParam}`),
    initialPageParam: 1,
    getNextPageParam: (last) =>
      last.page.page < last.page.pages ? last.page.page + 1 : undefined,
    enabled: !!user && !isAll,
  })

  // 拖动/滚动到页面约 90% 位置时自动触发 callback 加载更多讨论
  const isFetchingNextPage = main.isFetchingNextPage
  const hasNextPage = main.hasNextPage
  const fetchNextPage = main.fetchNextPage

  useEffect(() => {
    if (selectedId > 0 || isAll) return
    const container = document.getElementById("forum-content")
    if (!container) return

    let ticking = false
    const handleScrollAutoLoad = () => {
      if (ticking || !hasNextPage || isFetchingNextPage) return

      ticking = true
      requestAnimationFrame(() => {
        const { scrollTop, clientHeight, scrollHeight } = container
        // 当滑动深度达到 88%~90% 以上时自动加载下一页
        if (scrollHeight > clientHeight && (scrollTop + clientHeight) / scrollHeight >= 0.88) {
          void fetchNextPage()
        }
        ticking = false
      })
    }

    container.addEventListener("scroll", handleScrollAutoLoad, { passive: true })
    return () => container.removeEventListener("scroll", handleScrollAutoLoad)
  }, [selectedId, isAll, hasNextPage, isFetchingNextPage, fetchNextPage])

  const mainItems = main.data?.pages.flatMap((p) => p.items) ?? []
  const mainTotal = main.data?.pages[0]?.page.total

  // 分离版块内的置顶讨论与常规讨论
  const { pinnedItems, regularItems } = useMemo(() => {
    const pinned: typeof mainItems = []
    const regular: typeof mainItems = []
    for (const item of mainItems) {
      if (item.isSticky) {
        pinned.push(item)
      } else {
        regular.push(item)
      }
    }
    return { pinnedItems: pinned, regularItems: regular }
  }, [mainItems])

  return (
    <>
      <div className={selectedId > 0 ? "hidden" : "block"}>
        {isAll ? (
          <div>
        <div className="mb-4 flex items-center justify-between rounded-xl border border-border/70 bg-card p-4 shadow-xs">
          <div className="flex items-center gap-2.5">
            <Compass className="size-5 text-primary" />
            <h1 className="text-xl font-bold tracking-tight text-foreground">版块总览</h1>
            <span className="ml-1 rounded-full bg-secondary px-2.5 py-0.5 text-xs font-semibold text-secondary-foreground">
              {boards.length} 个版块
            </span>
          </div>
        </div>

        {homeLoading ? (
          <div className="grid gap-4 sm:grid-cols-2">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-44 rounded-xl" />
            ))}
          </div>
        ) : (
          <div className="grid gap-4 sm:grid-cols-2">
            {cards.map((c) => (
              <BoardCard
                key={c.tag.id}
                card={c}
                onSelect={selectBoard}
                onOpenDiscussion={openDiscussion}
              />
            ))}
          </div>
        )}
      </div>
    ) : (
      <div>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-border/70 bg-card p-4 shadow-xs">
        <div className="flex items-center gap-2.5">
          {activeTag?.color && (
            <span
              className="size-3.5 rounded-full shrink-0"
              style={{ backgroundColor: activeTag.color }}
            />
          )}
          <div>
            <h1 className="text-xl font-bold tracking-tight text-foreground">
              {activeTag?.name}
            </h1>
            {activeTag?.description && (
              <p className="mt-0.5 text-xs text-muted-foreground">
                {activeTag.description}
              </p>
            )}
          </div>
          {mainTotal !== undefined && (
            <span className="ml-1 rounded-full bg-secondary px-2.5 py-0.5 text-xs font-semibold text-secondary-foreground">
              {mainTotal} 篇
            </span>
          )}
        </div>

        <Button
          variant="outline"
          size="sm"
          onClick={() => selectBoard("all")}
          className="gap-1 text-xs"
        >
          ← 返回版块总览
        </Button>
      </div>

      {/* 版块内部讨论列表（紧凑高密度布局） */}
      <div className="space-y-2">
        {/* 置顶讨论独立折叠区 */}
        {pinnedItems.length > 0 && (
          <div className="overflow-hidden rounded-xl border border-amber-500/30 bg-amber-500/5">
            <div className="flex items-center justify-between border-b border-amber-500/20 px-4 py-2 text-xs font-semibold text-amber-800 dark:text-amber-300">
              <span className="flex items-center gap-1.5">
                <Pin className="size-3.5" /> 置顶公告
                <span className="rounded-full bg-amber-500/20 px-1.5 py-0.2 text-[10px]">
                  {pinnedItems.length}
                </span>
              </span>
              {pinnedItems.length > 2 && (
                <button
                  onClick={() => setExpandSticky(!expandSticky)}
                  className="flex items-center gap-1 text-[11px] font-normal hover:underline"
                >
                  {expandSticky ? (
                    <>
                      收起 <ChevronUp className="size-3" />
                    </>
                  ) : (
                    <>
                      展开所有置顶 ({pinnedItems.length}) <ChevronDown className="size-3" />
                    </>
                  )}
                </button>
              )}
            </div>
            <div className="p-2 sm:p-2.5 space-y-2">
              {(expandSticky ? pinnedItems : pinnedItems.slice(0, 2)).map((d) => (
                <DiscussionRowCompact key={d.id} d={d} onOpen={openDiscussion} />
              ))}
            </div>
          </div>
        )}

        {/* 常规帖子列表（高密度紧凑卡片） */}
        {main.isLoading ? (
          <div className="space-y-2">
            {Array.from({ length: 6 }).map((_, i) => (
              <Skeleton key={i} className="h-16 rounded-lg" />
            ))}
          </div>
        ) : (
          <>
            {regularItems.map((d) => (
              <DiscussionRowCompact key={d.id} d={d} onOpen={openDiscussion} />
            ))}

            {regularItems.length === 0 && pinnedItems.length === 0 && (
              <div className="rounded-xl border border-dashed border-border p-12 text-center text-sm text-muted-foreground">
                该版块暂无讨论，点击左上方发布第一篇帖子！
              </div>
            )}
          </>
        )}

        {/* 自动触底加载指示器与兜底手动按钮 */}
        {main.hasNextPage && (
          <div className="pt-2 pb-6 flex items-center justify-center text-xs text-muted-foreground">
            {main.isFetchingNextPage ? (
              <span className="inline-flex items-center gap-2 font-medium text-primary py-2">
                <span className="size-3.5 rounded-full border-2 border-primary border-t-transparent animate-spin" />
                正在加载更多讨论…
              </span>
            ) : (
              <button
                type="button"
                onClick={() => void main.fetchNextPage()}
                className="text-xs text-muted-foreground/60 hover:text-foreground py-2 transition-colors cursor-pointer"
              >
                下滑至 90% 自动加载，或点击此处加载更多
              </button>
            )}
          </div>
        )}

        {!main.hasNextPage && mainItems.length > 0 && (
          <div className="py-5 text-center text-xs text-muted-foreground/60">
            — 已经到底了，共 {mainTotal ?? mainItems.length} 篇讨论 —
          </div>
        )}
      </div>
    </div>
        )}
      </div>

      {selectedId > 0 && (
        <InlineDiscussion key={selectedId} id={selectedId} onBack={closeDiscussion} />
      )}
    </>
  )
}

// 现代化版块卡片（用于版块总览矩阵）
function BoardCard({
  card,
  onSelect,
  onOpenDiscussion,
}: {
  card: HomeCard
  onSelect: (slug: string) => void
  onOpenDiscussion: (id: number) => void
}) {
  const rawColor = card.tag.color?.trim()
  const color = rawColor && /^#[0-9a-fA-F]{3,8}$/.test(rawColor) ? rawColor : "#00838f"

  return (
    <Card
      className="group flex flex-col justify-between overflow-hidden transition-all hover:border-primary/40 hover:shadow-sm cursor-pointer"
      style={{ borderTop: `4px solid ${color}` }}
      onClick={() => onSelect(card.tag.slug)}
    >
      <CardHeader className="pb-2">
        <div className="flex items-start justify-between gap-2">
          <div className="flex items-center gap-2">
            <span className="size-3 rounded-full shrink-0" style={{ backgroundColor: color }} />
            <CardTitle className="text-base font-bold text-foreground group-hover:text-primary transition-colors">
              {card.tag.name}
            </CardTitle>
          </div>
          <span className="rounded-full bg-secondary px-2 py-0.5 text-xs font-semibold text-secondary-foreground">
            {card.tag.discussionsCount ?? 0} 帖
          </span>
        </div>
        {card.tag.description && (
          <p className="mt-1 text-xs text-muted-foreground line-clamp-2">
            {card.tag.description}
          </p>
        )}
      </CardHeader>

      <CardContent className="pt-0">
        {/* 子板块标签 */}
        {card.children && card.children.length > 0 && (
          <div className="mt-1 flex flex-wrap gap-1.5 pb-2">
            {card.children.map((ch) => (
              <TagChip key={ch.id} tag={ch} />
            ))}
          </div>
        )}

        {/* 板块最新讨论 */}
        {card.latest ? (
          <div
            onClick={(e) => {
              e.stopPropagation()
              const match = card.latest?.url.match(/\/d\/(\d+)/)
              if (match?.[1]) {
                onOpenDiscussion(Number(match[1]))
              } else {
                onSelect(card.tag.slug)
              }
            }}
            className="mt-2 flex items-center justify-between gap-2 rounded-lg bg-muted/50 px-2.5 py-2 text-xs transition-colors hover:bg-muted"
          >
            <div className="flex min-w-0 items-center gap-1.5">
              <span className="shrink-0 text-muted-foreground">最新:</span>
              <span className="truncate font-medium text-foreground hover:text-primary">
                {card.latest.title}
              </span>
            </div>
            <span className="shrink-0 text-muted-foreground text-[11px]">
              {timeAgo(card.latest.at)}
            </span>
          </div>
        ) : (
          <div className="mt-2 text-xs text-muted-foreground/80 py-1">暂无最新讨论</div>
        )}
      </CardContent>
    </Card>
  )
}
