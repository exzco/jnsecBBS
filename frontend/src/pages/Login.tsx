import { useState } from "react"
import { useMutation } from "@tanstack/react-query"
import { Link, useLocation, useNavigate, useSearchParams } from "react-router"
import { toast } from "sonner"
import { api, setCsrfToken } from "@/lib/api"
import type { User } from "@/lib/types"
import { useInvalidateSession, useSession } from "@/lib/session"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

export default function LoginPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const [searchParams] = useSearchParams()
  const invalidate = useInvalidateSession()
  const { data: session } = useSession()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")

  // 支持 location.state.from 与 query 参数 ?from= 或 ?redirect=
  const stateFrom = (location.state as { from?: string } | null)?.from
  const queryFrom = searchParams.get("from") || searchParams.get("redirect")
  const rawFrom = stateFrom || queryFrom || "/"
  // 安全校验：避免重定向至登录/注册造成死循环
  const safeFrom =
    rawFrom.startsWith("/") &&
    !rawFrom.startsWith("//") &&
    !rawFrom.startsWith("/login") &&
    !rawFrom.startsWith("/register")
      ? rawFrom
      : "/"

  const login = useMutation({
    mutationFn: () => api<{ user: User; csrfToken?: string }>("/api/auth/login", { json: { username, password } }),
    onSuccess: (data) => {
      if (data.csrfToken) setCsrfToken(data.csrfToken)
      invalidate()
      toast.success("登录成功")
      navigate(safeFrom, { replace: true })
    },
    onError: (e) => toast.error((e as Error).message),
  })

  return (
    <div className="mx-auto mt-10 max-w-sm">
      <Card>
        <CardHeader>
          <CardTitle>登录 {session?.siteName ?? ""}</CardTitle>
        </CardHeader>
        <CardContent>
          <form
            className="space-y-4"
            onSubmit={(e) => {
              e.preventDefault()
              if (username && password) login.mutate()
            }}
          >
            <div className="space-y-2">
              <Label htmlFor="username">用户名</Label>
              <Input
                id="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="password">密码</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                required
              />
            </div>
            <Button type="submit" className="w-full" disabled={login.isPending}>
              {login.isPending ? "登录中…" : "登录"}
            </Button>
          </form>
          {!session?.readOnly && session?.registration === "open" && (
            <p className="mt-4 text-center text-sm text-muted-foreground">
              还没有账号？
              <Link
                to={`/register?from=${encodeURIComponent(safeFrom)}`}
                state={{ from: safeFrom }}
                className="text-primary hover:underline font-medium"
              >
                立即注册
              </Link>
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
