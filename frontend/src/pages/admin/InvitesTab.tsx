import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Copy } from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import type { Invite } from "@/lib/types"
import { fmtDate } from "@/lib/format"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"

export function InvitesTab() {
  const qc = useQueryClient()
  const [count, setCount] = useState("1")
  const [days, setDays] = useState("")
  const { data } = useQuery({
    queryKey: ["adminInvites"],
    queryFn: () => api<{ invites: Invite[] }>("/api/admin/invites"),
  })

  const create = useMutation({
    mutationFn: () =>
      api<{ codes: string[] }>("/api/admin/invites", {
        json: { count: Number(count) || 1, expiresDays: Number(days) || 0 },
      }),
    onSuccess: async (r) => {
      void qc.invalidateQueries({ queryKey: ["adminInvites"] })
      try {
        await navigator.clipboard.writeText(r.codes.join("\n"))
        toast.success(`已生成 ${r.codes.length} 个邀请码（已复制）`)
      } catch {
        toast.success(`已生成 ${r.codes.length} 个邀请码（请手动复制）`)
      }
    },
    onError: (e) => toast.error((e as Error).message),
  })

  return (
    <div className="space-y-4">
      <Card className="flex flex-wrap items-end gap-3 p-4">
        <div className="space-y-1.5">
          <Label>数量</Label>
          <Input type="number" min={1} max={50} value={count} onChange={(e) => setCount(e.target.value)} className="w-24" />
        </div>
        <div className="space-y-1.5">
          <Label>有效天数（留空永久）</Label>
          <Input type="number" min={1} value={days} onChange={(e) => setDays(e.target.value)} className="w-36" />
        </div>
        <Button onClick={() => create.mutate()} disabled={create.isPending}>
          生成邀请码
        </Button>
      </Card>
      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>邀请码</TableHead>
              <TableHead>创建时间</TableHead>
              <TableHead>有效期</TableHead>
              <TableHead>状态</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(data?.invites ?? []).map((inv) => (
              <TableRow key={inv.code}>
                <TableCell>
                  <button
                    className="inline-flex items-center gap-1 font-mono hover:text-primary"
                    title="点击复制"
                    onClick={() => {
                      void navigator.clipboard?.writeText(inv.code)
                      toast.success("已复制")
                    }}
                  >
                    {inv.code} <Copy className="size-3" />
                  </button>
                </TableCell>
                <TableCell>{fmtDate(inv.createdAt)}</TableCell>
                <TableCell>{inv.expiresAt ? fmtDate(inv.expiresAt) : "永久"}</TableCell>
                <TableCell>
                  {inv.usedAt ? (
                    <span className="text-xs text-muted-foreground">已使用</span>
                  ) : inv.expired ? (
                    <span className="text-xs font-semibold text-red-600">已过期</span>
                  ) : (
                    <span className="text-xs font-semibold text-green-600">可用</span>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Card>
    </div>
  )
}
