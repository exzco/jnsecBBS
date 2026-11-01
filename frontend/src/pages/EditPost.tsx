import { useQuery } from "@tanstack/react-query"
import { Navigate, useParams } from "react-router"
import { api } from "@/lib/api"

export default function EditPostPage() {
  const { id = "" } = useParams()
  const { data, isError, error } = useQuery({
    queryKey: ["postEdit", id],
    queryFn: () => api<{ id: number; page: number; discussion: { id: number } }>(`/api/posts/${id}/edit`),
    retry: false,
  })
  if (isError) return <p role="alert" className="py-20 text-center text-destructive">{error.message}</p>
  if (!data) return <p className="py-20 text-center text-muted-foreground">加载中…</p>
  return <Navigate to={`/?d=${data.discussion.id}&page=${data.page}&edit=${data.id}`} replace />
}
