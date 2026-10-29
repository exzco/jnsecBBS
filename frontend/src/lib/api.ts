let csrfToken = ""

export function setCsrfToken(t: string) {
  csrfToken = t
}

export class ApiError extends Error {
  status: number
  constructor(message: string, status: number) {
    super(message)
    this.status = status
  }
}

interface Options {
  method?: "GET" | "POST"
  json?: unknown
  form?: FormData
}

export async function api<T = unknown>(path: string, opts: Options = {}): Promise<T> {
  const method = opts.method ?? (opts.json !== undefined || opts.form ? "POST" : "GET")
  const headers: Record<string, string> = {}
  if (opts.json !== undefined) headers["Content-Type"] = "application/json"
  if (method !== "GET") headers["X-CSRF-Token"] = csrfToken

  const res = await fetch(path, {
    method,
    headers,
    credentials: "same-origin",
    body: opts.json !== undefined ? JSON.stringify(opts.json) : opts.form,
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new ApiError((data as { error?: string }).error ?? `请求失败（${res.status}）`, res.status)
  }
  return data as T
}

// URL 组合：/d/{id}-{slug}
export function discussionUrl(id: number, slug: string): string {
  return slug ? `/d/${id}-${slug}` : `/d/${id}`
}
