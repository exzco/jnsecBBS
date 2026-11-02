import { useEffect, useRef, useState, type FormEvent } from "react"
import { Link, useLocation, useNavigate } from "react-router"
import { useTheme } from "next-themes"
import {
  Bell,
  ChevronLeft,
  LogOut,
  Moon,
  Search,
  ShieldCheck,
  SquarePen,
  Sun,
  X,
} from "lucide-react"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
import { Input } from "@/components/ui/input"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { api } from "@/lib/api"
import { useInvalidateSession, useSession, useUnread } from "@/lib/session"
import { toast } from "sonner"

// 顶栏：teal 渐变（沿用旧站风格）。
// 左侧提供「返回主页」按钮并配置 Tooltip 消除歧义；
// 移动端优化响应式布局，支持移动端紧凑搜索条展开，精简图标展示。
export function Navbar({ siteName }: { siteName: string }) {
  const { data: session } = useSession()
  const user = session?.user ?? null
  const { data: unread = 0 } = useUnread(!!user)
  const [q, setQ] = useState("")
  const [mobileSearchOpen, setMobileSearchOpen] = useState(false)
  const mobileInputRef = useRef<HTMLInputElement>(null)
  const navigate = useNavigate()
  const location = useLocation()
  const invalidate = useInvalidateSession()
  const { resolvedTheme, setTheme } = useTheme()
  const currentPath = location.pathname + location.search

  // 移动端展开搜索栏时自动 focus
  useEffect(() => {
    if (mobileSearchOpen) {
      setTimeout(() => mobileInputRef.current?.focus(), 50)
    }
  }, [mobileSearchOpen])

  // 路由变化时自动收起移动端搜索栏
  useEffect(() => {
    setMobileSearchOpen(false)
  }, [location.pathname])

  function submitSearch(e: FormEvent) {
    e.preventDefault()
    if (q.trim()) {
      navigate(`/search?q=${encodeURIComponent(q.trim())}`)
      setMobileSearchOpen(false)
    }
  }

  async function logout() {
    try {
      await api("/api/auth/logout", { method: "POST" })
      invalidate()
      navigate("/")
      toast.success("已退出登录")
    } catch (err) {
      toast.error((err as Error).message)
    }
  }

  return (
    <header
      className="sticky top-0 z-50 shrink-0 shadow-md"
      style={{ background: "linear-gradient(90deg, #005661, #00838f)" }}
    >
      <div className="flex h-14 w-full items-center px-3 sm:px-6 2xl:px-8">
        {/* 移动端全宽搜索条状态 */}
        {mobileSearchOpen ? (
          <form
            onSubmit={submitSearch}
            className="flex w-full items-center gap-2 animate-in fade-in-0 duration-150"
          >
            <button
              type="button"
              aria-label="关闭搜索"
              onClick={() => setMobileSearchOpen(false)}
              className="inline-flex size-8 shrink-0 items-center justify-center rounded-md text-white/90 hover:bg-white/10 hover:text-white"
            >
              <ChevronLeft className="size-5" />
            </button>
            <div className="relative flex-1">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-white/70" />
              <Input
                ref={mobileInputRef}
                value={q}
                onChange={(e) => setQ(e.target.value)}
                placeholder="搜索讨论、帖子或用户…"
                className="h-9 w-full rounded-full border-none bg-white/20 pl-8 pr-8 text-sm text-white placeholder:text-white/60 focus-visible:ring-2 focus-visible:ring-white/60"
              />
              {q && (
                <button
                  type="button"
                  aria-label="清除内容"
                  onClick={() => setQ("")}
                  className="absolute right-2.5 top-1/2 -translate-y-1/2 text-white/70 hover:text-white"
                >
                  <X className="size-3.5" />
                </button>
              )}
            </div>
            <button
              type="submit"
              className="shrink-0 text-sm font-medium text-white px-2 py-1 rounded-md hover:bg-white/10"
            >
              搜索
            </button>
          </form>
        ) : (
          /* 常规顶栏布局 */
          <div className="flex w-full items-center gap-2 sm:gap-3">
            {/* 左侧：返回主页按钮（带 Tooltip 消除歧义） */}
            <Tooltip>
              <TooltipTrigger asChild>
                <Link
                  to="/"
                  aria-label="返回主页"
                  className="inline-flex size-8 shrink-0 items-center justify-center rounded-md text-white/90 transition-colors hover:bg-white/10 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/60"
                >
                  <ChevronLeft className="size-5" />
                </Link>
              </TooltipTrigger>
              <TooltipContent side="bottom" sideOffset={6}>
                返回主页
              </TooltipContent>
            </Tooltip>

            {/* 站点 Logo / 名字 */}
            <Link
              to="/"
              className="shrink-0 text-base font-bold tracking-tight text-white transition-opacity hover:opacity-90 sm:text-lg"
            >
              {siteName}
            </Link>

            {/* 桌面端内联搜索框（小屏下隐藏） */}
            <form onSubmit={submitSearch} className="relative hidden max-w-sm flex-1 sm:block">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-white/80" />
              <Input
                value={q}
                onChange={(e) => setQ(e.target.value)}
                placeholder="搜索…"
                className="h-8 rounded-full border-none bg-white/20 pl-8 text-white placeholder:text-white/60 focus-visible:border-transparent focus-visible:ring-2 focus-visible:ring-white/50"
              />
            </form>

            {/* 右侧导航区 */}
            <nav className="ml-auto flex items-center gap-1.5 sm:gap-3 lg:gap-4">
              {/* 移动端搜索展开触发按钮 */}
              <button
                type="button"
                aria-label="搜索"
                title="搜索"
                onClick={() => setMobileSearchOpen(true)}
                className="inline-flex size-8 items-center justify-center rounded-md text-white/90 transition-colors hover:bg-white/10 hover:text-white sm:hidden"
              >
                <Search className="size-4.5" />
              </button>

              {/* 切换主题 */}
              <Tooltip>
                <TooltipTrigger asChild>
                  <button
                    aria-label="切换主题"
                    className="inline-flex size-8 items-center justify-center rounded-md text-white/90 transition-colors hover:bg-white/10 hover:text-white"
                    onClick={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}
                  >
                    {resolvedTheme === "dark" ? <Sun className="size-4.5 sm:size-5" /> : <Moon className="size-4.5 sm:size-5" />}
                  </button>
                </TooltipTrigger>
                <TooltipContent side="bottom" sideOffset={6}>
                  {resolvedTheme === "dark" ? "切换为浅色模式" : "切换为深色模式"}
                </TooltipContent>
              </Tooltip>

              {user ? (
                <>
                  {/* 发帖按钮（仅大屏） */}
                  {!session?.readOnly && (
                    <Link
                      to="/post"
                      className="hidden items-center gap-1 text-sm font-medium text-white/95 hover:text-white hover:underline sm:inline-flex"
                    >
                      <SquarePen className="size-4" /> 发帖
                    </Link>
                  )}

                  {/* 管理后台 */}
                  {user.isAdmin && !session?.readOnly && (
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <Link
                          to="/admin"
                          className="inline-flex items-center gap-1 text-sm font-medium text-white/95 hover:text-white hover:underline p-1 sm:p-0"
                          aria-label="管理后台"
                        >
                          <ShieldCheck className="size-4.5 sm:size-4" />
                          <span className="hidden sm:inline">管理后台</span>
                        </Link>
                      </TooltipTrigger>
                      <TooltipContent side="bottom" sideOffset={6} className="sm:hidden">
                        管理后台
                      </TooltipContent>
                    </Tooltip>
                  )}

                  {/* 消息通知 */}
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <Link
                        to="/notifications"
                        className="relative inline-flex size-8 items-center justify-center rounded-md text-white/95 transition-colors hover:bg-white/10 hover:text-white"
                        aria-label="通知"
                      >
                        <Bell className="size-4.5 sm:size-5" />
                        {unread > 0 && (
                          <span className="absolute right-0.5 top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-red-500 px-1 text-[10px] font-bold text-white shadow-xs">
                            {unread > 99 ? "99+" : unread}
                          </span>
                        )}
                      </Link>
                    </TooltipTrigger>
                    <TooltipContent side="bottom" sideOffset={6}>
                      通知 {unread > 0 ? `(${unread})` : ""}
                    </TooltipContent>
                  </Tooltip>

                  {/* 个人主页头像 */}
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <Link
                        to={`/u/${user.username}`}
                        aria-label={`${user.username} 的个人主页`}
                        className="rounded-full outline-none transition-shadow hover:ring-2 hover:ring-white/70 focus-visible:ring-2 focus-visible:ring-white"
                      >
                        <Avatar className="size-7 sm:size-8 ring-2 ring-white/50">
                          <AvatarImage src={user.avatar} alt={user.username} />
                          <AvatarFallback>{user.username[0]}</AvatarFallback>
                        </Avatar>
                      </Link>
                    </TooltipTrigger>
                    <TooltipContent side="bottom" sideOffset={6}>
                      个人主页 ({user.username})
                    </TooltipContent>
                  </Tooltip>

                  {/* 登出按钮：手机端仅显示图标，桌面端显示文字 */}
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <button
                        onClick={logout}
                        aria-label="退出登录"
                        className="inline-flex items-center gap-1 rounded-md p-1.5 text-sm font-medium text-white/95 transition-colors hover:bg-white/10 hover:text-white hover:underline sm:p-1"
                      >
                        <LogOut className="size-4" />
                        <span className="hidden sm:inline">登出</span>
                      </button>
                    </TooltipTrigger>
                    <TooltipContent side="bottom" sideOffset={6}>
                      退出登录
                    </TooltipContent>
                  </Tooltip>
                </>
              ) : (
                /* 未登录状态 */
                <div className="flex items-center gap-2 sm:gap-3">
                  {!session?.readOnly && (
                    <Link
                      to={`/register?from=${encodeURIComponent(currentPath)}`}
                      state={{ from: currentPath }}
                      className="text-xs font-medium text-white/90 hover:text-white hover:underline sm:text-sm"
                    >
                      注册
                    </Link>
                  )}
                  <Link
                    to={`/login?from=${encodeURIComponent(currentPath)}`}
                    state={{ from: currentPath }}
                    className="text-xs font-medium text-white/90 hover:text-white hover:underline sm:text-sm"
                  >
                    登录
                  </Link>
                </div>
              )}
            </nav>
          </div>
        )}
      </div>
    </header>
  )
}
