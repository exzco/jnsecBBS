import { lazy, Suspense } from "react"
import { BrowserRouter, Link, Navigate, Route, Routes, useLocation, useParams } from "react-router"
import { ThemeProvider } from "next-themes"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { Toaster } from "@/components/ui/sonner"
import { TooltipProvider } from "@/components/ui/tooltip"
import { Navbar } from "@/components/Navbar"
import { ForumLayout } from "@/components/ForumLayout"
import { useSession } from "@/lib/session"
import { Button } from "@/components/ui/button"
import { cn } from "cn"

const HomePage = lazy(() => import("@/pages/Home"))
const DiscussionPage = lazy(() => import("@/pages/Discussion"))
const NewPostPage = lazy(() => import("@/pages/NewPost"))
const EditPostPage = lazy(() => import("@/pages/EditPost"))
const LoginPage = lazy(() => import("@/pages/Login"))
const RegisterPage = lazy(() => import("@/pages/Register"))
const ProfilePage = lazy(() => import("@/pages/Profile"))
const SettingsPage = lazy(() => import("@/pages/Settings"))
const NotificationsPage = lazy(() => import("@/pages/Notifications"))
const SearchPage = lazy(() => import("@/pages/Search"))
const AdminPage = lazy(() => import("@/pages/Admin"))

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, refetchOnWindowFocus: false },
  },
})

function NotFound() {
  return (
    <div className="py-24 text-center">
      <h1 className="text-2xl font-bold">页面不存在</h1>
      <p className="mt-2 text-muted-foreground">你访问的页面不存在或已被删除。</p>
      <Button asChild className="mt-4">
        <Link to="/">返回首页</Link>
      </Button>
    </div>
  )
}

// 内容类页面守卫：未登录一律跳登录页（携带来源，登录后回跳）
// 板块主页已并入首页三栏：旧链接 /t/{slug} 重定向到首页并选中对应板块
function TagRedirect() {
  const { slug = "" } = useParams()
  return <Navigate to={`/?board=${encodeURIComponent(slug)}`} replace />
}

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { data, isLoading } = useSession()
  const location = useLocation()
  if (isLoading) return <div className="py-20 text-center text-muted-foreground">加载中…</div>
  if (!data?.user) {
    return <Navigate to="/login" state={{ from: location.pathname + location.search }} replace />
  }
  return <>{children}</>
}

function RequireAdmin({ children }: { children: React.ReactNode }) {
  const { data, isLoading } = useSession()
  if (isLoading) return <div className="py-20 text-center text-muted-foreground">加载中…</div>
  if (!data?.user) return <LoginPage />
  if (!data.user.isAdmin) return <NotFound />
  return <>{children}</>
}

function RequireWritable({ children }: { children: React.ReactNode }) {
  const { data, isLoading } = useSession()
  if (isLoading) return <div className="py-20 text-center text-muted-foreground">加载中…</div>
  if (data?.readOnly) return <p className="py-12 text-center text-muted-foreground">迁移期间新站只读，请前往旧站进行修改。</p>
  return <>{children}</>
}

function Shell() {
  const { data } = useSession()
  const siteName = data?.siteName ?? "JNSEC BBS"
  const location = useLocation()
  const isAuthOrAdmin =
    location.pathname === "/login" ||
    location.pathname === "/register" ||
    location.pathname.startsWith("/admin")

  return (
    <div className="h-[100dvh] max-h-[100dvh] flex flex-col bg-background text-foreground overflow-hidden">
      <Navbar siteName={siteName} />
      <main
        className={cn(
          "w-full px-2 sm:px-6 2xl:px-8 flex-1 min-h-0",
          !isAuthOrAdmin
            ? "py-2 sm:py-3 flex flex-col overflow-hidden"
            : "py-5 overflow-y-auto [scrollbar-gutter:stable]",
        )}
      >
        {data?.readOnly && (
          <div role="status" className="mb-3 shrink-0 rounded-lg border border-primary/20 bg-primary/5 px-4 py-2 text-sm">
            迁移期间新站只读，每 5 分钟同步旧站数据。
            <a href="/switch-to-old" className="ml-2 font-medium text-primary underline underline-offset-2">前往旧站发布或修改</a>
          </div>
        )}
        <Suspense fallback={<div className="py-20 text-center text-muted-foreground">加载中…</div>}>
          <Routes>
            {/* 统一三栏社区容器：首页、详情、发帖、编辑、个人主页、设置、通知、搜索 */}
            <Route element={<ForumLayout />}>
              <Route path="/" element={<RequireAuth><HomePage /></RequireAuth>} />
              <Route path="/t/:slug" element={<RequireAuth><TagRedirect /></RequireAuth>} />
              <Route path="/d/:idSlug" element={<RequireAuth><DiscussionPage /></RequireAuth>} />
              <Route path="/post" element={<RequireAuth><RequireWritable><NewPostPage /></RequireWritable></RequireAuth>} />
              <Route path="/posts/:id/edit" element={<RequireAuth><RequireWritable><EditPostPage /></RequireWritable></RequireAuth>} />
              <Route path="/u/:username" element={<RequireAuth><ProfilePage /></RequireAuth>} />
              <Route path="/settings" element={<RequireAuth><RequireWritable><SettingsPage /></RequireWritable></RequireAuth>} />
              <Route path="/notifications" element={<RequireAuth><NotificationsPage /></RequireAuth>} />
              <Route path="/search" element={<RequireAuth><SearchPage /></RequireAuth>} />
            </Route>

            {/* 独立认证与管理全屏页面 */}
            <Route path="/login" element={<LoginPage />} />
            <Route path="/register" element={<RequireWritable><RegisterPage /></RequireWritable>} />
            <Route path="/admin" element={<RequireAdmin><RequireWritable><AdminPage /></RequireWritable></RequireAdmin>} />
            <Route path="*" element={<NotFound />} />
          </Routes>
        </Suspense>
      </main>
    </div>
  )
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider attribute="class" defaultTheme="system" enableSystem>
        <TooltipProvider>
          <BrowserRouter>
            <Shell />
            <Toaster position="top-center" richColors />
          </BrowserRouter>
        </TooltipProvider>
      </ThemeProvider>
    </QueryClientProvider>
  )
}
