import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Ban, Search } from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import type { AdminUserRow } from "@/lib/types"
import type { AdminMeta } from "../Admin"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { cn } from "cn"

export function UsersTab({ meta }: { meta?: AdminMeta }) {
  const qc = useQueryClient()
  const [q, setQ] = useState("")
  const [query, setQuery] = useState("")
  const { data } = useQuery({
    queryKey: ["adminUsers", query],
    queryFn: () => api<{ users: AdminUserRow[] }>(`/api/admin/users?q=${encodeURIComponent(query)}`),
  })

  const save = useMutation({
    mutationFn: ({ id, body }: { id: number; body: Record<string, unknown> }) =>
      api(`/api/admin/users/${id}`, { json: body }),
    onMutate: async ({ id, body }) => {
      await qc.cancelQueries({ queryKey: ["adminUsers"] })
      const previous = qc.getQueryData<{ users: AdminUserRow[] }>(["adminUsers", query])
      if (previous && (body.action === "ban" || body.action === "unban")) {
        const isBanned = body.action === "ban"
        qc.setQueryData<{ users: AdminUserRow[] }>(["adminUsers", query], {
          users: previous.users.map((u) =>
            u.user.id === id ? { ...u, user: { ...u.user, isBanned } } : u,
          ),
        })
      }
      return { previous }
    },
    onError: (e, _vars, context) => {
      if (context?.previous) {
        qc.setQueryData(["adminUsers", query], context.previous)
      }
      toast.error((e as Error).message)
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ["adminUsers"] })
      toast.success("已保存")
    },
  })

  return (
    <div className="space-y-4">
      <form
        className="flex max-w-md gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          setQuery(q)
        }}
      >
        <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索用户名 / 邮箱" />
        <Button type="submit" variant="outline" className="gap-1.5">
          <Search className="size-4" /> 搜索
        </Button>
      </form>
      <Card className="overflow-x-auto p-0">
        <Table className="table-fixed min-w-[760px]">
          <TableHeader>
            <TableRow>
              <TableHead className="w-16">ID</TableHead>
              <TableHead className="w-36">用户名</TableHead>
              <TableHead className="w-52">邮箱</TableHead>
              <TableHead className="w-48">用户组</TableHead>
              <TableHead className="w-28">主题/回复</TableHead>
              <TableHead className="w-24">状态</TableHead>
              <TableHead className="w-28">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(data?.users ?? []).map((row) => (
              <TableRow key={row.user.id}>
                <TableCell className="w-16 font-mono text-xs">{row.user.id}</TableCell>
                <TableCell className="w-36 font-medium truncate">{row.user.username}</TableCell>
                <TableCell className="w-52 text-muted-foreground truncate">{row.email}</TableCell>
                <TableCell className="w-48">
                  <div className="flex flex-wrap items-center gap-2">
                    {meta?.groups
                      .filter((g) => g.id !== 2)
                      .map((g) => (
                        <label key={g.id} className="flex items-center gap-1 text-xs">
                          <Switch
                            checked={row.groupIds.includes(g.id)}
                            onCheckedChange={() => {
                              const next = row.groupIds.includes(g.id)
                                ? row.groupIds.filter((x) => x !== g.id)
                                : [...row.groupIds, g.id]
                              save.mutate({ id: row.user.id, body: { action: "groups", groups: next } })
                            }}
                          />
                          {g.name}
                        </label>
                      ))}
                  </div>
                </TableCell>
                <TableCell className="w-28 text-xs">
                  {row.user.discussionsCount} / {row.user.postsCount}
                </TableCell>
                <TableCell className="w-24">
                  <span
                    className={cn(
                      "inline-block w-14 text-xs font-semibold",
                      row.user.isBanned ? "text-red-600" : "text-green-600",
                    )}
                  >
                    {row.user.isBanned ? "已封禁" : "正常"}
                  </span>
                </TableCell>
                <TableCell className="w-28">
                  <Button
                    variant="outline"
                    size="sm"
                    className={cn(
                      "w-[76px] justify-center gap-1.5",
                      row.user.isBanned
                        ? "text-foreground hover:bg-muted"
                        : "text-red-600 hover:bg-red-500/10 hover:text-red-600",
                    )}
                    onClick={() =>
                      save.mutate({
                        id: row.user.id,
                        body: { action: row.user.isBanned ? "unban" : "ban" },
                      })
                    }
                  >
                    <Ban className="size-3.5 shrink-0" />
                    <span>{row.user.isBanned ? "解封" : "封禁"}</span>
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Card>
    </div>
  )
}
