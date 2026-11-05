import { useEffect, useState } from "react"
import { useMutation } from "@tanstack/react-query"
import { Link, useLocation, useNavigate, useSearchParams } from "react-router"
import { RefreshCw } from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import type { User } from "@/lib/types"
import { useInvalidateSession, useSession } from "@/lib/session"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

export default function RegisterPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const [searchParams] = useSearchParams()
  const invalidate = useInvalidateSession()
  const { data: session } = useSession()
  const inviteMode = session?.registration !== "open"
  const [form, setForm] = useState({
    invite: "",
    username: "",
    email: "",
    password: "",
    passwordConfirm: "",
    captchaId: "",
    captchaValue: "",
  })

  // 支持 location.state.from 与 query 参数 ?from= 或 ?redirect=
  const stateFrom = (location.state as { from?: string } | null)?.from
  const queryFrom = searchParams.get("from") || searchParams.get("redirect")
  const rawFrom = stateFrom || queryFrom || "/"
  const safeFrom =
    rawFrom.startsWith("/") &&
    !rawFrom.startsWith("//") &&
    !rawFrom.startsWith("/login") &&
    !rawFrom.startsWith("/register")
      ? rawFrom
      : "/"

  // 开放注册时预取一个验证码
  useEffect(() => {
    if (!inviteMode) newCaptcha()
  }, [inviteMode])

  async function newCaptcha() {
    try {
      const r = await api<{ id: string }>("/api/auth/captcha")
      setForm((f) => ({ ...f, captchaId: r.id, captchaValue: "" }))
    } catch (e) {
      toast.error("获取验证码失败: " + (e as Error).message)
    }
  }

  const register = useMutation({
    mutationFn: () =>
      api<{ user: User }>("/api/auth/register", {
        json: {
          username: form.username,
          email: form.email,
          password: form.password,
          passwordConfirm: form.passwordConfirm,
          invite: form.invite,
          captchaId: form.captchaId,
          captchaValue: form.captchaValue,
        },
      }),
    onSuccess: () => {
      invalidate()
      toast.success("注册成功，欢迎加入！")
      navigate(safeFrom, { replace: true })
    },
    onError: (e) => {
      toast.error((e as Error).message)
      if (!inviteMode) void newCaptcha()
    },
  })

  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm((f) => ({ ...f, [k]: e.target.value }))

  return (
    <div className="mx-auto mt-10 max-w-sm">
      <Card>
        <CardHeader>
          <CardTitle>注册 {session?.siteName ?? ""}</CardTitle>
        </CardHeader>
        <CardContent>
          <form
            className="space-y-4"
            onSubmit={(e) => {
              e.preventDefault()
              if (form.password !== form.passwordConfirm) {
                toast.error("两次输入的密码不一致")
                return
              }
              register.mutate()
            }}
          >
            {inviteMode && (
              <div className="space-y-2">
                <Label htmlFor="invite">邀请码</Label>
                <Input
                  id="invite"
                  value={form.invite}
                  onChange={set("invite")}
                  required
                  placeholder="请输入邀请码"
                />
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor="username">用户名</Label>
              <Input
                id="username"
                value={form.username}
                onChange={set("username")}
                autoComplete="username"
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="email">邮箱</Label>
              <Input
                id="email"
                type="email"
                value={form.email}
                onChange={set("email")}
                autoComplete="email"
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="password">密码</Label>
              <Input
                id="password"
                type="password"
                value={form.password}
                onChange={set("password")}
                autoComplete="new-password"
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="passwordConfirm">确认密码</Label>
              <Input
                id="passwordConfirm"
                type="password"
                value={form.passwordConfirm}
                onChange={set("passwordConfirm")}
                autoComplete="new-password"
                required
              />
            </div>
            {!inviteMode && (
              <div className="space-y-2">
                <Label htmlFor="captchaValue">验证码</Label>
                <div className="flex items-center gap-2">
                  {form.captchaId && (
                    <img
                      src={`/api/auth/captcha?id=${form.captchaId}`}
                      alt="验证码"
                      className="h-9 w-28 rounded-md border bg-muted object-cover"
                      onClick={newCaptcha}
                      title="点击刷新验证码"
                    />
                  )}
                  <Input
                    id="captchaValue"
                    value={form.captchaValue}
                    onChange={set("captchaValue")}
                    required
                    autoComplete="off"
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    onClick={newCaptcha}
                    aria-label="刷新验证码"
                  >
                    <RefreshCw />
                  </Button>
                </div>
              </div>
            )}
            <Button type="submit" className="w-full" disabled={register.isPending}>
              {register.isPending ? "注册中…" : "注册"}
            </Button>
          </form>
          <p className="mt-4 text-center text-sm text-muted-foreground">
            已有账号？
            <Link
              to={`/login?from=${encodeURIComponent(safeFrom)}`}
              state={{ from: safeFrom }}
              className="text-primary hover:underline font-medium"
            >
              直接登录
            </Link>
          </p>
        </CardContent>
      </Card>
    </div>
  )
}
