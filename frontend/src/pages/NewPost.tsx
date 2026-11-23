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

function loadDraft() {
  try {
    const raw = localStorage.getItem("bbs_draft_new_post")
    if (raw) {
      const d = JSON.parse(raw)
      return {
        title: typeof d.title === "string" ? d.title : "",
        content: typeof d.content === "string" ? d.content : "",
        tagIds: Array.isArray(d.tagIds) ? d.tagIds : [],
      }
    }
  } catch {
    // ignore storage error
  }
  return { title: "", content: "", tagIds: [] }
}

export default function NewPostPage() {
  const navigate = useNavigate()
  const [initialDraft] = useState(loadDraft)
  const [title, setTitle] = useState(initialDraft.title)
  const [content, setContent] = useState(initialDraft.content)
  const [tagIds, setTagIds] = useState<number[]>(initialDraft.tagIds)

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

  const { data, isLoading } = useQuery({
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
      navigate(r.url)
    },
    onError: (e) => toast.error((e as Error).message),
  })

  function toggleParent(g: PostForm["groups"][number]) {
    const childIds = g.children.map((c) => c.id)
    const isSelected = tagIds.includes(g.parent.id) || childIds.some((id) => tagIds.includes(id))
    if (isSelected) {
      setTagIds((cur) => cur.filter((id) => id !== g.parent.id && !childIds.includes(id)))
    } else {
      if (tagIds.length >= 5) {
        toast.warning("最多只能选择 5 个版块/标签")
        return
      }
      setTagIds((cur) => [...cur, g.parent.id])
    }
  }

  function toggleChild(g: PostForm["groups"][number], childId: number) {
    if (tagIds.includes(childId)) {
      setTagIds((cur) => cur.filter((id) => id !== childId))
    } else {
      const needParent = !tagIds.includes(g.parent.id)
      const additionalCount = needParent ? 2 : 1
      if (tagIds.length + additionalCount > 5) {
        toast.warning("最多只能选择 5 个版块/标签")
        return
      }
      setTagIds((cur) => (needParent ? [...cur, g.parent.id, childId] : [...cur, childId]))
    }
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
            <Label>版块与标签（可多选）</Label>
            {!isLoading && (data?.groups.length ?? 0) === 0 && (
              <Alert>
                <AlertDescription>没有可选的版块，请联系管理员。</AlertDescription>
              </Alert>
            )}
            <div className="space-y-3">
              {data?.groups.map((g) => {
                const childIds = g.children.map((c) => c.id)
                const isParentSelected =
                  tagIds.includes(g.parent.id) || childIds.some((id) => tagIds.includes(id))
                const hasChildren = g.children.length > 0
                return (
                  <div
                    key={g.parent.id}
                    className="rounded-lg border border-border/60 bg-muted/20 p-3 space-y-2.5 transition-colors"
                  >
                    <div className="flex flex-wrap items-center gap-2">
                      <label
                        className={`cursor-pointer rounded-full border px-3 py-1 text-xs font-semibold transition-all ${
                          isParentSelected
                            ? "border-primary bg-primary text-primary-foreground shadow-xs"
                            : "hover:bg-secondary border-border bg-card text-foreground"
                        }`}
                      >
                        <input
                          type="checkbox"
                          className="hidden"
                          checked={isParentSelected}
                          onChange={() => toggleParent(g)}
                        />
                        {g.parent.name}
                      </label>
                      <span className="text-[11px] text-muted-foreground">
                        {hasChildren ? "（主版块，子标签可选）" : "（主版块）"}
                      </span>
                    </div>

                    {hasChildren && (
                      <div className="flex flex-wrap items-center gap-2 pl-2 border-l-2 border-border/60">
                        {g.children.map((t) => {
                          const isChildSelected = tagIds.includes(t.id)
                          return (
                            <label
                              key={t.id}
                              className={`cursor-pointer rounded-full border px-2.5 py-0.5 text-xs font-medium transition-colors ${
                                isChildSelected
                                  ? "border-primary bg-primary/90 text-primary-foreground shadow-xs"
                                  : "hover:bg-secondary border-border/80 bg-background text-muted-foreground hover:text-foreground"
                              }`}
                            >
                              <input
                                type="checkbox"
                                className="hidden"
                                checked={isChildSelected}
                                onChange={() => toggleChild(g, t.id)}
                              />
                              {t.name}
                            </label>
                          )
                        })}
                      </div>
                    )}
                  </div>
                )
              })}
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
