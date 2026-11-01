import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link, useSearchParams } from "react-router"
import { ChevronLeft, Search as SearchIcon } from "lucide-react"
import { api, discussionUrl } from "@/lib/api"
import type { DiscussionSummary, Post } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { timeAgo } from "@/lib/format"

type Result = {
  q: string
  discussions: DiscussionSummary[]
  posts: { post: Post; discussion: DiscussionSummary }[]
}

function Highlight({ text, q }: { text: string; q: string }) {
  if (!q.trim()) return <>{text}</>
  const parts: (string | { hit: string })[] = []
  const lower = text.toLowerCase()
  const ql = q.toLowerCase()
  let pos = 0
  for (;;) {
    const i = lower.indexOf(ql, pos)
    if (i < 0) {
      parts.push(text.slice(pos))
      break
    }
    if (i > pos) parts.push(text.slice(pos, i))
    parts.push({ hit: text.slice(i, i + q.length) })
    pos = i + q.length
  }
  return (
    <>
      {parts.map((p, i) =>
        typeof p === "string" ? (
          <span key={i}>{p}</span>
        ) : (
          <mark key={i} className="rounded bg-yellow-200/70 px-0.5 dark:bg-yellow-500/40">
            {p.hit}
          </mark>
        ),
      )}
    </>
  )
}

export default function SearchPage() {
  const [params, setParams] = useSearchParams()
  const q = params.get("q") ?? ""
  const [input, setInput] = useState(q)

  const { data } = useQuery({
    queryKey: ["search", q],
    enabled: q.length >= 2,
    queryFn: () => api<Result>(`/api/search?q=${encodeURIComponent(q)}`),
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <Button asChild variant="ghost" size="sm" className="-ml-2 gap-1 text-muted-foreground hover:text-foreground">
          <Link to="/">
            <ChevronLeft className="size-4" /> 返回论坛
          </Link>
        </Button>
        <h1 className="text-xl font-bold tracking-tight text-foreground">搜索</h1>
      </div>
      <form
        className="mb-5 flex gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          if (input.trim()) setParams({ q: input.trim() })
        }}
      >
        <Input value={input} onChange={(e) => setInput(e.target.value)} placeholder="输入关键词，搜索主题与回复…" />
        <Button type="submit" className="gap-1.5">
          <SearchIcon className="size-4" /> 搜索
        </Button>
      </form>

      {q.length >= 2 ? (
        <>
          <h2 className="mb-2 mt-4 text-lg font-bold">主题</h2>
          <div>
            {(data?.discussions.length ?? 0) === 0 ? (
              <p className="p-8 text-center text-sm text-muted-foreground">没有匹配的主题。</p>
            ) : (
              data!.discussions.map((d) => (
                <div key={d.id} className="border-b px-4 py-3 last:border-b-0">
                  <Link to={discussionUrl(d.id, d.slug)} className="font-semibold hover:text-primary">
                    <Highlight text={d.title} q={q} />
                  </Link>
                  <div className="mt-1 flex items-center gap-2 text-xs text-muted-foreground">
                    {d.tags.map((t) => (
                      <span key={t.id}>{t.name}</span>
                    ))}
                    <time>{timeAgo(d.lastPostedAt)}</time>
                  </div>
                </div>
              ))
            )}
          </div>

          <h2 className="mb-2 mt-4 text-lg font-bold">回复</h2>
          <div>
            {(data?.posts.length ?? 0) === 0 ? (
              <p className="p-8 text-center text-sm text-muted-foreground">没有匹配的回复。</p>
            ) : (
              data!.posts.map(({ post, discussion }) => (
                <div key={post.id} className="border-b px-4 py-3 last:border-b-0">
                  <Link
                    to={`${discussionUrl(discussion.id, discussion.slug)}#post-${post.id}`}
                    className="font-semibold hover:text-primary"
                  >
                    <Highlight text={discussion.title} q={q} />
                  </Link>
                  <p className="mt-1 line-clamp-2 text-sm text-muted-foreground">
                    <Highlight text={post.content.slice(0, 140)} q={q} />
                  </p>
                  <time className="mt-1 block text-xs text-muted-foreground">{timeAgo(post.createdAt)}</time>
                </div>
              ))
            )}
          </div>
        </>
      ) : (
        <p className="py-8 text-center text-sm text-muted-foreground">输入至少 2 个字符开始搜索。</p>
      )}
    </div>
  )
}
