import { useState, type FormEvent } from "react"
import { Link, useLocation, useNavigate } from "react-router"
import { useTheme } from "next-themes"
import { Bell, ChevronLeft, LogOut, Moon, Search, SquarePen, Sun } from "lucide-react"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
import { Input } from "@/components/ui/input"
import { api } from "@/lib/api"
import { useInvalidateSession, useSession, useUnread } from "@/lib/session"
import { toast } from "sonner"

// 顶栏：teal 渐变（沿用旧站风格）。未登录为「注册 / 登录」文字链接；
// 登录后为「发帖 · 通知 · 头像(点击进个人主页) · 登出」。
export function Navbar({ siteName }: { siteName: string }) {
  const { data: session } = useSession()
  const user = session?.user ?? null
  const { data: unread = 0 } = useUnread(!!user)
  const [q, setQ] = useState("")
  const navigate = useNavigate()
  const location = useLocation()
  const invalidate = useInvalidateSession()
  const { resolvedTheme, setTheme } = useTheme()
  const currentPath = location.pathname + location.search

  function submitSearch(e: FormEvent) {
    e.preventDefault()
    if (q.trim()) navigate(`/search?q=${encodeURIComponent(q.trim())}`)
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
      <div className="mx-auto flex h-14 w-full max-w-[1780px] items-center gap-3 px-3 sm:px-6 lg:px-8">
        <button
          aria-label="后退"
          title="后退"
          onClick={() => navigate(-1)}
          className="shrink-0 text-white/90 transition-colors hover:text-white"
        >
          <ChevronLeft className="size-5" />
        </button>
        <Link to="/" className="shrink-0 text-lg font-bold tracking-tight text-white">
          {siteName}
        </Link>

        <form onSubmit={submitSearch} className="relative max-w-sm flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-white/80" />
          <Input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="搜索…"
            className="h-8 rounded-full border-none bg-white/20 pl-8 text-white placeholder:text-white/60 focus-visible:border-transparent focus-visible:ring-2 focus-visible:ring-white/50"
          />
        </form>

        <nav className="ml-auto flex items-center gap-4">
          <button
            aria-label="切换主题"
            className="text-white/90 transition-colors hover:text-white"
            onClick={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}
          >
            {resolvedTheme === "dark" ? <Sun className="size-5" /> : <Moon className="size-5" />}
          </button>

          {user ? (
            <>
              {!session?.readOnly && <Link
                to="/post"
                className="hidden items-center gap-1 text-sm font-medium text-white/95 hover:text-white hover:underline sm:inline-flex"
              >
                <SquarePen className="size-4" /> 发帖
              </Link>}
              <Link to="/notifications" className="relative text-white/95 hover:text-white" aria-label="通知">
                <Bell className="size-5" />
                {unread > 0 && (
                  <span className="absolute -right-2 -top-1.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-red-500 px-1 text-[10px] font-bold text-white">
                    {unread > 99 ? "99+" : unread}
                  </span>
                )}
              </Link>
              <Link
                to={`/u/${user.username}`}
                title={`${user.username} 的个人主页`}
                className="rounded-full outline-none transition-shadow hover:ring-2 hover:ring-white/70 focus-visible:ring-2 focus-visible:ring-white"
              >
                <Avatar className="size-8 ring-2 ring-white/50">
                  <AvatarImage src={user.avatar} alt={user.username} />
                  <AvatarFallback>{user.username[0]}</AvatarFallback>
                </Avatar>
              </Link>
              <button
                onClick={logout}
                className="inline-flex items-center gap-1 text-sm font-medium text-white/95 hover:text-white hover:underline"
              >
                <LogOut className="size-4" /> 登出
              </button>
            </>
          ) : (
            <>
              {!session?.readOnly && (
                <Link
                  to={`/register?from=${encodeURIComponent(currentPath)}`}
                  state={{ from: currentPath }}
                  className="text-sm font-medium text-white/90 hover:text-white hover:underline"
                >
                  注册
                </Link>
              )}
              <Link
                to={`/login?from=${encodeURIComponent(currentPath)}`}
                state={{ from: currentPath }}
                className="text-sm font-medium text-white/90 hover:text-white hover:underline"
              >
                登录
              </Link>
            </>
          )}
        </nav>
      </div>
    </header>
  )
}
