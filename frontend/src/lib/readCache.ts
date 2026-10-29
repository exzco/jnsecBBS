import type { QueryClient, InfiniteData } from "@tanstack/react-query"
import type { RecentData } from "@/lib/types"

/**
 * 同步更新客户端 React Query 缓存中特定讨论的已读状态。
 * 当用户点开某个帖子或内嵌查看详情时调用，使得退出/返回列表时该帖子的未读小圆点立即消失。
 */
export function markDiscussionAsReadInCache(qc: QueryClient, discussionId: number) {
  if (!discussionId) return

  // 1. 同步更新首页最新信息流缓存
  qc.setQueriesData<InfiniteData<RecentData>>({ queryKey: ["recent", "feed"] }, (old) => {
    if (!old?.pages) return old
    return {
      ...old,
      pages: old.pages.map((page) => ({
        ...page,
        items: page.items?.map((item) =>
          item.id === discussionId ? { ...item, hasUnread: false } : item,
        ),
      })),
    }
  })

  // 2. 同步更新所有板块独立讨论流缓存
  qc.setQueriesData<InfiniteData<RecentData>>({ queryKey: ["tag-discussions"] }, (old) => {
    if (!old?.pages) return old
    return {
      ...old,
      pages: old.pages.map((page) => ({
        ...page,
        items: page.items?.map((item) =>
          item.id === discussionId ? { ...item, hasUnread: false } : item,
        ),
      })),
    }
  })

  // 3. 同步更新右侧最新文章侧边栏缓存
  qc.setQueriesData<RecentData>({ queryKey: ["recent", "sidebar"] }, (old) => {
    if (!old?.items) return old
    return {
      ...old,
      items: old.items.map((item) =>
        item.id === discussionId ? { ...item, hasUnread: false } : item,
      ),
    }
  })
}
