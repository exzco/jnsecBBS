import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Plus } from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import type { AdminTagNode, Tag } from "@/lib/types"
import type { AdminMeta } from "../Admin"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"

export function TagsTab({ meta }: { meta?: AdminMeta }) {
  const qc = useQueryClient()
  const { data } = useQuery({
    queryKey: ["adminTags"],
    queryFn: () => api<{ nodes: AdminTagNode[] }>("/api/admin/tags"),
  })

  const save = useMutation({
    mutationFn: (body: Record<string, unknown>) => api("/api/admin/tags", { json: body }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["adminTags"] })
      void qc.invalidateQueries({ queryKey: ["adminMeta"] })
      void qc.invalidateQueries({ queryKey: ["home"] })
      toast.success("已保存")
    },
    onError: (e) => toast.error((e as Error).message),
  })
  const del = useMutation({
    mutationFn: (id: number) => api(`/api/admin/tags/${id}/delete`, { method: "POST" }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["adminTags"] })
      toast.success("已删除")
    },
    onError: (e) => toast.error((e as Error).message),
  })

  return (
    <div className="space-y-4">
      <TagEditForm meta={meta} onSubmit={(body) => save.mutate(body)} />
      {(data?.nodes ?? []).map((node) => (
        <div key={node.tag.id}>
          <TagNodeRow node={node} meta={meta} onSave={(b) => save.mutate({ ...b, id: node.tag.id })} onDelete={() => del.mutate(node.tag.id)} />
          {node.children.map((ch) => (
            <div key={ch.tag.id} className="ml-6 border-l pl-4">
              <TagNodeRow node={ch} meta={meta} onSave={(b) => save.mutate({ ...b, id: ch.tag.id })} onDelete={() => del.mutate(ch.tag.id)} />
            </div>
          ))}
        </div>
      ))}
    </div>
  )
}

function TagNodeRow({
  node, meta, onSave, onDelete,
}: {
  node: AdminTagNode
  meta?: AdminMeta
  onSave: (body: Record<string, unknown>) => void
  onDelete: () => void
}) {
  const [open, setOpen] = useState(false)
  const t = node.tag
  return (
    <Card className="mb-2 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="size-3 rounded" style={{ background: t.color || "#888" }} />
        <b>{t.name}</b>
        <code className="text-xs text-muted-foreground">/t/{t.slug}</code>
        <span className="text-xs text-muted-foreground">{t.discussionsCount} 主题</span>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button variant="outline" size="sm" className="ml-auto">
              编辑
            </Button>
          </DialogTrigger>
          <DialogContent className="max-w-xl">
            <DialogHeader>
              <DialogTitle>编辑版块「{t.name}」</DialogTitle>
            </DialogHeader>
            <TagEditForm
              meta={meta}
              initial={{ ...node, id: t.id }}
              onSubmit={(b) => {
                onSave(b)
                setOpen(false)
              }}
            />
          </DialogContent>
        </Dialog>
        <Button
          variant="outline"
          size="sm"
          className="text-red-600"
          onClick={() => confirm(`确定删除版块「${t.name}」？`) && onDelete()}
        >
          删除
        </Button>
      </div>
    </Card>
  )
}

function TagEditForm({
  meta, initial, onSubmit,
}: {
  meta?: AdminMeta
  initial?: { id?: number; tag: Tag }
  onSubmit: (body: Record<string, unknown>) => void
}) {
  const editing = !!initial?.id
  const t = initial?.tag
  const [form, setForm] = useState({
    name: t?.name ?? "",
    slug: t?.slug ?? "",
    color: t?.color ?? "",
    position: String(t?.position ?? 10),
    parentId: "0",
    description: t?.description ?? "",
  })

  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault()
        onSubmit({
          id: initial?.id ?? 0,
          name: form.name,
          slug: form.slug,
          color: form.color,
          position: Number(form.position) || 0,
          parentId: Number(form.parentId) || 0,
          description: form.description,
        })
      }}
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label>名称</Label>
          <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} required />
        </div>
        <div className="space-y-1.5">
          <Label>URL 标识（留空自动生成）</Label>
          <Input value={form.slug} onChange={(e) => setForm({ ...form, slug: e.target.value })} />
        </div>
        <div className="space-y-1.5">
          <Label>颜色（如 #1e88e5）</Label>
          <Input value={form.color} onChange={(e) => setForm({ ...form, color: e.target.value })} placeholder="#1e88e5" />
        </div>
        <div className="space-y-1.5">
          <Label>排序</Label>
          <Input type="number" value={form.position} onChange={(e) => setForm({ ...form, position: e.target.value })} />
        </div>
        {!editing && (
          <div className="space-y-1.5">
            <Label>父版块</Label>
            <Select value={form.parentId} onValueChange={(v) => setForm({ ...form, parentId: v })}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="0">无（作为大版块）</SelectItem>
                {meta?.topTags.map((t) => (
                  <SelectItem key={t.id} value={String(t.id)}>
                    {t.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}
        <div className="space-y-1.5">
          <Label>描述</Label>
          <Input value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
        </div>
      </div>
      <div className="flex justify-end">
        <Button type="submit">
          <Plus className="size-4" /> {editing ? "保存" : "创建版块"}
        </Button>
      </div>
    </form>
  )
}
