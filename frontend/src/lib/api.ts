let csrfToken = ""
let refreshingCsrf: Promise<string> | null = null

export function setCsrfToken(t: string) {
  if (t) csrfToken = t
}

async function refreshCsrfToken(): Promise<string> {
  if (!refreshingCsrf) {
    refreshingCsrf = fetch("/api/me", {
      credentials: "same-origin",
      cache: "no-store",
    })
      .then(async (res) => {
        if (!res.ok) throw new ApiError("无法刷新会话，请刷新页面重试。", res.status)
        return res.json()
      })
      .then((data: { csrfToken?: string }) => {
        if (typeof data.csrfToken !== "string" || !data.csrfToken) {
          throw new ApiError("会话响应缺少 CSRF Token，请刷新页面重试。", 502)
        }
        csrfToken = data.csrfToken
        return csrfToken
      })
      .finally(() => {
        refreshingCsrf = null
      })
  }
  return refreshingCsrf
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
  keepalive?: boolean
}

export async function api<T = unknown>(path: string, opts: Options = {}): Promise<T> {
  const method = opts.method ?? (opts.json !== undefined || opts.form ? "POST" : "GET")

  const send = async (retried: boolean): Promise<T> => {
    if (method !== "GET" && !csrfToken) {
      await refreshCsrfToken()
    }
    const headers: Record<string, string> = {}
    if (opts.json !== undefined) headers["Content-Type"] = "application/json"
    if (method !== "GET") headers["X-CSRF-Token"] = csrfToken

    const res = await fetch(path, {
      method,
      headers,
      credentials: "same-origin",
      cache: method === "GET" ? "no-store" : undefined,
      keepalive: opts.keepalive,
      body: opts.json !== undefined ? JSON.stringify(opts.json) : opts.form,
    })
    const data = (await res.json().catch(() => ({}))) as { error?: string; code?: string }
    if (!res.ok) {
      // CSRF Cookie 过期或轮换时，403 响应已下发新 Cookie；重拉一次 /api/me 同步 token 并重试一次。
      if (!retried && method !== "GET" && res.status === 403 && data.code === "csrf_invalid") {
        await refreshCsrfToken()
        return send(true)
      }
      throw new ApiError(data.error ?? `请求失败（${res.status}）`, res.status)
    }
    return data as T
  }

  return send(false)
}

// URL 组合：/d/{id}-{slug}
export function discussionUrl(id: number, slug: string): string {
  return slug ? `/d/${id}-${slug}` : `/d/${id}`
}
