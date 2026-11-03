import { useEffect, useState } from "react"
import { useMutation, useQuery } from "@tanstack/react-query"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { api } from "@/lib/api"
import type { PostForm } from "@/lib/types"
import { MarkdownEditor } from "@/components/MarkdownEditor"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Alert, AlertDescription } from "@/components/ui/alert"

export default function NewPostPage() {
  const navigate = useNavigate()
  const [title, setTitle] = useState(() => {
    try {
      const draft = JSON.parse(localStorage.getItem("bbs_draft_new_post") || "{}")
      return draft.title || ""
    } catch {
      return ""
    }
  })
  const [content, setContent] = useState(() => {
    try {
      const draft = JSON.parse(localStorage.getItem("bbs_draft_new_post") || "{}")
      return draft.content || ""
    } catch {
      return ""
    }
  })
  const [tagIds, setTagIds] = useState<number[]>(() => {
    try {
      const draft = JSON.parse(localStorage.getItem("bbs_draft_new_post") || "{}")
      return Array.isArray(draft.tagIds) ? draft.tagIds : []
    } catch {
      return []
    }
  })

  // 监听输入变动，自动保存草稿至本地缓存
  useEffect(() => {
    try {
      if (title.trim() || content.trim() || tagIds.length > 0) {
        localStorage.setItem(
          "bbs_draft_new_post",
          JSON.stringify({ title, content, tagIds, updatedAt: Date.now() })
        )
      } else {
        localStorage.removeItem("bbs_draft_new_post")
      }
    } catch {
      // ignore
    }
  }, [title, content, tagIds])

  const clearDraft = () => {
    try {
      localStorage.removeItem("bbs_draft_new_post")
    } catch {
      // ignore
    }
    setTitle("")
    setContent("")
    setTagIds([])
    toast.info("已清空草稿")
  }

  const { data } = useQuery({
    queryKey: ["postForm"],
    queryFn: () => api<PostForm>("/api/post-form"),
  })

  const create = useMutation({
    mutationFn: () => api<{ id: number; url: string }>("/api/discussions", { json: { title, content, tagIds } }),
    onSuccess: (r) => {
      toast.success("发布成功")
      try {
        localStorage.removeItem("bbs_draft_new_post")
      } catch {
        // ignore
      }
      void api; // noop 避免移除导入
      navigate(r.url)
    },
    onError: (e) => toast.error((e as Error).message),
  })

  function toggleTag(id: number) {
    setTagIds((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]))
  }

  const hasDraft = Boolean(title.trim() || content.trim() || tagIds.length > 0)

  return (
    <div className="mx-auto max-w-3xl">
      <h1 className="mb-4 text-xl font-bold">发新帖</h1>
      <Card>
        <CardHeader>
          <CardTitle>主题信息</CardTitle>
        </CardHeader>
        <CardContent className="space-y-5">
          <div className="space-y-2">
            <Label htmlFor="title">标题</Label>
            <Input
              id="title"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              minLength={3}
              maxLength={200}
              placeholder="请输入标题（3-200 字）"
            />
          </div>

          <div className="space-y-2">
            <Label>版块（可多选）</Label>
            {(data?.groups.length ?? 0) === 0 && (
              <Alert>
                <AlertDescription>没有可选的版块，请联系管理员。</AlertDescription>
              </Alert>
            )}
            <div className="space-y-3">
              {data?.groups.map((g) => (
                <div key={g.parent.id}>
                  <div className="mb-1.5 text-xs font-semibold text-muted-foreground">{g.parent.name}</div>
                  <div className="flex flex-wrap gap-2">
                    {g.children.map((t) => (
                      <label
                        key={t.id}
                        className={`cursor-pointer rounded-full border px-3 py-1 text-xs font-medium transition-colors ${
                          tagIds.includes(t.id)
                            ? "border-primary bg-primary text-primary-foreground"
                            : "hover:bg-secondary"
                        }`}
                      >
                        <input
                          type="checkbox"
                          className="hidden"
                          checked={tagIds.includes(t.id)}
                          onChange={() => toggleTag(t.id)}
                        />
                        {t.name}
                      </label>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          </div>

          <div className="space-y-2">
            <Label>正文</Label>
            <MarkdownEditor value={content} onChange={setContent} rows={12} />
          </div>

          <div className="flex items-center justify-between pt-2">
            <div className="flex items-center gap-2">
              {hasDraft && (
                <>
                  <span className="rounded bg-muted px-2 py-0.5 text-xs text-muted-foreground">
                    已自动保存草稿
                  </span>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="h-7 text-xs text-muted-foreground hover:text-destructive"
                    onClick={clearDraft}
                  >
                    清空草稿
                  </Button>
                </>
              )}
            </div>
            <Button
              onClick={() => create.mutate()}
              disabled={create.isPending || !title.trim() || !content.trim() || tagIds.length === 0}
            >
              发布
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
