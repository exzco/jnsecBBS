// 全局会话：/api/me 查询 + CSRF token 注入 + 登录态快捷方法
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { api, setCsrfToken } from "@/lib/api"
import type { SessionInfo, User } from "@/lib/types"

export function useSession() {
  return useQuery({
    queryKey: ["me"],
    queryFn: async () => {
      const data = await api<SessionInfo>("/api/me")
      setCsrfToken(data.csrfToken)
      return data
    },
    staleTime: 30_000,
    refetchOnWindowFocus: true,
  })
}

// 未读通知数：已登录时每分钟轮询，切回窗口时同步刷新
export function useUnread(enabled: boolean) {
  return useQuery({
    queryKey: ["unread"],
    enabled,
    refetchInterval: 60_000,
    refetchOnWindowFocus: true,
    queryFn: async () => {
      const data = await api<{ user: User | null; unread?: number }>("/api/me")
      return data.unread ?? 0
    },
  })
}

export function useInvalidateSession() {
  const qc = useQueryClient()
  return () => {
    void qc.invalidateQueries({ queryKey: ["me"] })
    void qc.invalidateQueries({ queryKey: ["unread"] })
  }
}
