import { useEffect, useRef } from "react"
import renderMathInElement from "katex/contrib/auto-render"

// 服务端已消毒的 HTML 渲染容器，集成 KaTeX LaTeX 数学公式渲染
export function MarkdownBody({ html }: { html: string }) {
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!containerRef.current) return
    try {
      renderMathInElement(containerRef.current, {
        delimiters: [
          { left: "$$", right: "$$", display: true },
          { left: "$", right: "$", display: false },
          { left: "\\(", right: "\\)", display: false },
          { left: "\\[", right: "\\]", display: true },
        ],
        throwOnError: false,
        ignoredTags: ["script", "noscript", "style", "textarea", "pre", "code"],
      })
    } catch (e) {
      console.warn("KaTeX render error:", e)
    }
  }, [html])

  return (
    <div
      ref={containerRef}
      className="md-body text-[15px] leading-relaxed break-words"
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}
