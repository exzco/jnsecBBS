import { useRef, useState } from "react"
import { Link } from "react-router"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { Camera, ChevronLeft, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { useSession } from "@/lib/session"
import { UserAvatar } from "@/components/DiscussionParts"
import { AvatarCropperModal } from "@/components/AvatarCropperModal"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import type { User } from "@/lib/types"

// 个人资料表单：子组件仅在 me 就绪后挂载，避免 useState 初值在会话
// 加载完成前被固化为空（旧实现签名框总是空的）。
function ProfileForm({ me, realName, blogUrl, readOnly }: { me: User; realName: string; blogUrl: string; readOnly: boolean }) {
  const qc = useQueryClient()
  const [bio, setBio] = useState(me.bio ?? "")
  const [name, setName] = useState(realName ?? "")
  const [blog, setBlog] = useState(blogUrl ?? "")

  const saved = useMutation({
    mutationFn: (body: Record<string, unknown>) => api("/api/me/profile", { json: body }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["me"] })
      toast.success("已保存")
    },
    onError: (e) => toast.error((e as Error).message),
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle>个人资料</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-2">
          <Label htmlFor="bio">签名档</Label>
          <Textarea
            id="bio"
            value={bio}
            onChange={(e) => setBio(e.target.value)}
            rows={3}
            maxLength={500}
            disabled={readOnly}
            placeholder="一句话介绍自己（500 字以内）"
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="realName">实名</Label>
          <Input
            id="realName"
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={30}
            disabled={readOnly}
            placeholder="将展示在个人主页（30 字以内）"
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="blogUrl">博客链接</Label>
          <Input
            id="blogUrl"
            value={blog}
            onChange={(e) => setBlog(e.target.value)}
            maxLength={255}
            disabled={readOnly}
            placeholder="https://example.com（留空则不展示）"
          />
        </div>
        <div className="flex items-center justify-between gap-3">
          {readOnly ? (
            <p className="text-xs text-muted-foreground">迁移期间新站只读，暂不能修改资料。</p>
          ) : (
            <span />
          )}
          <Button
            onClick={() => saved.mutate({ bio, realName: name, blogUrl: blog })}
            disabled={saved.isPending || readOnly}
          >
            保存资料
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

// 邮箱绑定：展示当前绑定邮箱，换绑需当前密码确认；老用户注册邮箱即初始绑定，无需重复绑定。
function EmailForm({ email, readOnly }: { email?: string; readOnly: boolean }) {
  const qc = useQueryClient()
  const [addr, setAddr] = useState(email ?? "")
  const [pwd, setPwd] = useState("")

  const changeEmail = useMutation({
    mutationFn: (body: Record<string, unknown>) => api("/api/me/email", { json: body }),
    onSuccess: () => {
      setPwd("")
      void qc.invalidateQueries({ queryKey: ["me"] })
      toast.success("邮箱已更新，回帖通知将发送到新邮箱")
    },
    onError: (e) => toast.error((e as Error).message),
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle>邮箱绑定</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-2">
          <Label htmlFor="email">绑定邮箱</Label>
          <Input
            id="email"
            type="email"
            value={addr}
            onChange={(e) => setAddr(e.target.value)}
            maxLength={255}
            disabled={readOnly}
            placeholder="回帖通知将发送到此邮箱"
          />
          {email && <p className="text-xs text-muted-foreground">当前绑定：{email}</p>}
        </div>
        <div className="space-y-2">
          <Label htmlFor="emailPwd">当前密码</Label>
          <Input
            id="emailPwd"
            type="password"
            value={pwd}
            onChange={(e) => setPwd(e.target.value)}
            autoComplete="current-password"
          />
        </div>
        <div className="flex items-center justify-between gap-3">
          {readOnly ? (
            <p className="text-xs text-muted-foreground">迁移期间新站只读，暂不能修改。</p>
          ) : (
            <span />
          )}
          <Button
            onClick={() => changeEmail.mutate({ email: addr, password: pwd })}
            disabled={changeEmail.isPending || readOnly || !addr || !pwd || addr === email}
          >
            更新绑定
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

export default function SettingsPage() {
  const { data: session } = useSession()
  const qc = useQueryClient()
  const me = session?.user
  const readOnly = !!session?.readOnly
  const fileRef = useRef<HTMLInputElement>(null)
  const [pwd, setPwd] = useState({ current: "", next: "", confirm: "" })
  const [cropModalOpen, setCropModalOpen] = useState(false)
  const [cropImageSrc, setCropImageSrc] = useState<string | null>(null)

  const changePwd = useMutation({
    mutationFn: (body: Record<string, unknown>) => api("/api/me/password", { json: body }),
    onSuccess: () => {
      setPwd({ current: "", next: "", confirm: "" })
      toast.success("密码已修改，其他设备已退出登录")
    },
    onError: (e) => toast.error((e as Error).message),
  })

  const avatar = useMutation({
    mutationFn: (form: FormData) => api<{ avatar: string }>("/api/me/avatar", { form }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["me"] })
      toast.success("头像已更新")
    },
    onError: (e) => toast.error((e as Error).message),
  })

  const handleCropConfirm = (blob: Blob) => {
    const fd = new FormData()
    fd.append("file", blob, "avatar.png")
    avatar.mutate(fd, {
      onSuccess: () => {
        setCropModalOpen(false)
        setCropImageSrc(null)
      },
    })
  }

  if (!me) return null

  return (
    <div className="mx-auto max-w-2xl space-y-5">
      <div className="flex items-center gap-2">
        <Button asChild variant="ghost" size="sm" className="-ml-2 gap-1 text-muted-foreground hover:text-foreground">
          <Link to="/">
            <ChevronLeft className="size-4" /> 返回论坛
          </Link>
        </Button>
        <h1 className="text-xl font-bold tracking-tight text-foreground">账号设置</h1>
      </div>

      <ProfileForm
        key={me.id}
        me={me}
        realName={session?.realName ?? ""}
        blogUrl={session?.blogUrl ?? ""}
        readOnly={readOnly}
      />

      <Card>
        <CardHeader>
          <CardTitle>头像</CardTitle>
        </CardHeader>
        <CardContent className="flex items-center gap-5">
          <UserAvatar user={me} className="size-20" />
          <div className="space-y-2">
            <input
              ref={fileRef}
              type="file"
              accept="image/png,image/jpeg"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0]
                if (f) {
                  if (f.size > 10 * 1024 * 1024) {
                    toast.error("图片文件过大，请选择不超过 10MB 的图片。")
                    return
                  }
                  const reader = new FileReader()
                  reader.onload = () => {
                    setCropImageSrc(reader.result as string)
                    setCropModalOpen(true)
                  }
                  reader.readAsDataURL(f)
                }
                e.target.value = ""
              }}
            />
            <div className="flex gap-2">
              <Button variant="outline" size="sm" className="gap-1.5" onClick={() => fileRef.current?.click()}>
                <Camera className="size-3.5" /> 上传头像
              </Button>
              {me.avatar.startsWith("/avatars/") && (
                <Button
                  variant="outline"
                  size="sm"
                  className="gap-1.5 text-red-600"
                  onClick={() => {
                    const fd = new FormData()
                    fd.append("action", "remove")
                    avatar.mutate(fd)
                  }}
                >
                  <Trash2 className="size-3.5" /> 恢复默认
                </Button>
              )}
            </div>
            <p className="text-xs text-muted-foreground">PNG / JPG，支持自选裁剪范围，生成 128×128 高清头像</p>
          </div>
        </CardContent>
      </Card>

      <AvatarCropperModal
        open={cropModalOpen}
        imageSrc={cropImageSrc}
        isUploading={avatar.isPending}
        onClose={() => {
          setCropModalOpen(false)
          setCropImageSrc(null)
        }}
        onConfirm={handleCropConfirm}
      />

      <EmailForm email={session?.email} readOnly={readOnly} />

      <Card>
        <CardHeader>
          <CardTitle>修改密码</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="current">当前密码</Label>
            <Input
              id="current"
              type="password"
              value={pwd.current}
              onChange={(e) => setPwd({ ...pwd, current: e.target.value })}
              autoComplete="current-password"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="next">新密码</Label>
            <Input
              id="next"
              type="password"
              value={pwd.next}
              onChange={(e) => setPwd({ ...pwd, next: e.target.value })}
              minLength={8}
              autoComplete="new-password"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="confirm">确认新密码</Label>
            <Input
              id="confirm"
              type="password"
              value={pwd.confirm}
              onChange={(e) => setPwd({ ...pwd, confirm: e.target.value })}
              minLength={8}
              autoComplete="new-password"
            />
          </div>
          <div className="flex justify-end">
            <Button
              onClick={() => changePwd.mutate(pwd)}
              disabled={changePwd.isPending || !pwd.current || !pwd.next}
            >
              修改密码
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
