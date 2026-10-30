import Markdown from "react-markdown"
import remarkMath from "remark-math"
import remarkGfm from "remark-gfm"
import rehypeKatex from "rehype-katex"
import rehypeRaw from "rehype-raw"
import "katex/dist/katex.min.css"

export interface MarkdownBodyProps {
  content?: string
  html?: string
  children?: string
}

// 预处理旧版 Flarum 特殊标签与楼层引用
function preprocessMarkdown(source: string): string {
  if (!source) return ""

  // 1. 转换 [upl-image-preview url=...] 为标准 Markdown 图片语法
  let text = source.replace(/\[upl-image-preview\s+url=["']?([^"'\s\]]+)["']?\s*\]/gi, "![]($1)")

  // 2. 转换 [upl-file uuid=... size=...]文件名[/upl-file] 为下载链接
  text = text.replace(
    /\[upl-file\s+uuid=["']?([a-zA-Z0-9\-]+)["']?(?:\s+size=["']?([^"'\s\]]+)["']?)?\s*\]([\s\S]*?)\[\/upl-file\]/gi,
    (_m, uuid, size, name) => {
      const filename = (name || "附件").trim()
      const sizeStr = (size || "").trim()
      return sizeStr ? `[📥 ${filename} (${sizeStr})](/api/fof/download/${uuid})` : `[📥 ${filename}](/api/fof/download/${uuid})`
    }
  )

  // 3. 转换 @"用户名"#p123 或 @用户名#123 为楼层引用链接
  text = text.replace(/@(?:"([^"]+)"|([a-zA-Z0-9_\-]{2,20}))\s*#p?(\d+)/g, (_m, name1, name2, num) => {
    const name = name1 || name2
    return `[@${name} #${num}](#post-${num})`
  })

  return text
}

// 纯前端 Markdown + LaTeX 数学公式渲染容器
export function MarkdownBody({ content, html, children }: MarkdownBodyProps) {
  const source = content ?? children ?? html ?? ""
  const processed = preprocessMarkdown(source)

  return (
    <div className="md-body text-[15px] leading-relaxed break-words">
      <Markdown
        remarkPlugins={[remarkGfm, remarkMath]}
        rehypePlugins={[rehypeRaw, [rehypeKatex, { throwOnError: false }]]}
      >
        {processed}
      </Markdown>
    </div>
  )
}
