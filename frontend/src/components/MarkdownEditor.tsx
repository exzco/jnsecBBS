import { useRef, useState } from "react"
import { Eye, Image, Loader2, Paperclip, PenLine, Sigma } from "lucide-react"
import { toast } from "sonner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { Button } from "@/components/ui/button"
import { api } from "@/lib/api"
import { MarkdownBody } from "@/components/MarkdownBody"

interface UploadResponse {
  url: string
  uuid: string
  name: string
  size: number
  isImage: boolean
  markdown: string
}

// 编写 + 预览 + 图片/附件上传
// 粘贴 + LaTeX 插入
export function MarkdownEditor({
  id,
  value,
  onChange,
  placeholder,
  rows = 10,
}: {
  id?: string
  value: string
  onChange: (v: string) => void
  placeholder?: string
  rows?: number
}) {
  const [tab, setTab] = useState("write")
  const [uploading, setUploading] = useState(false)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const imgInputRef = useRef<HTMLInputElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  function switchPreview(next: string) {
    setTab(next)
  }

  // 在光标位置插入文本
  function insertAtCursor(text: string) {
    const el = textareaRef.current
    if (!el) {
      onChange(value ? `${value}\n\n${text}` : text)
      return
    }
    const start = el.selectionStart ?? value.length
    const end = el.selectionEnd ?? value.length
    const before = value.substring(0, start)
    const after = value.substring(end)
    const prefix = before.length > 0 && !before.endsWith("\n") && !before.endsWith(" ") ? "\n" : ""
    const suffix = after.length > 0 && !after.startsWith("\n") && !after.startsWith(" ") ? "\n" : ""
    const nextVal = `${before}${prefix}${text}${suffix}${after}`
    onChange(nextVal)

    setTimeout(() => {
      el.focus()
      const newCursor = start + prefix.length + text.length
      el.setSelectionRange(newCursor, newCursor)
    }, 0)
  }

  // 上传文件处理
  async function handleUpload(file: File) {
    if (file.size > 10 * 1024 * 1024) {
      toast.error("文件过大，不能超过 10 MB。")
      return
    }
    const fd = new FormData()
    fd.append("file", file)
    setUploading(true)
    try {
      const res = await api<UploadResponse>("/api/upload", { form: fd })
      insertAtCursor(res.markdown)
      toast.success(`${res.isImage ? "图片" : "附件"}成功上传`)
    } catch (e) {
      toast.error((e as Error).message || "上传失败，请重试。")
    } finally {
      setUploading(false)
    }
  }

  // 粘贴图片自动上传
  function handlePaste(e: React.ClipboardEvent<HTMLTextAreaElement>) {
    const items = e.clipboardData?.items
    if (!items) return
    for (let i = 0; i < items.length; i++) {
      const item = items[i]
      if (item.kind === "file" && item.type.startsWith("image/")) {
        const file = item.getAsFile()
        if (file) {
          e.preventDefault()
          void handleUpload(file)
          return
        }
      }
    }
  }

  // 拖拽文件进入编辑框上传
  function handleDrop(e: React.DragEvent<HTMLTextAreaElement>) {
    const files = e.dataTransfer.files
    if (files && files.length > 0) {
      e.preventDefault()
      for (let i = 0; i < files.length; i++) {
        void handleUpload(files[i])
      }
    }
  }

  return (
    <Tabs value={tab} onValueChange={switchPreview}>
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <TabsList>
          <TabsTrigger value="write">
            <PenLine data-slot="icon" /> 编写
          </TabsTrigger>
          <TabsTrigger value="preview">
            <Eye data-slot="icon" /> 预览
          </TabsTrigger>
        </TabsList>

        <div className="flex items-center gap-1.5">
          <input
            ref={imgInputRef}
            type="file"
            accept="image/*"
            className="hidden"
            onChange={(e) => {
              const f = e.target.files?.[0]
              if (f) void handleUpload(f)
              e.target.value = ""
            }}
          />
          <input
            ref={fileInputRef}
            type="file"
            className="hidden"
            onChange={(e) => {
              const f = e.target.files?.[0]
              if (f) void handleUpload(f)
              e.target.value = ""
            }}
          />

          <Button
            type="button"
            variant="outline"
            size="xs"
            onClick={() => imgInputRef.current?.click()}
            disabled={uploading}
            className="gap-1 text-xs"
            title="上传图片（支持粘贴截图）"
          >
            {uploading ? <Loader2 className="size-3.5 animate-spin" /> : <Image className="size-3.5" />}
            图片
          </Button>

          <Button
            type="button"
            variant="outline"
            size="xs"
            onClick={() => fileInputRef.current?.click()}
            disabled={uploading}
            className="gap-1 text-xs"
            title="上传附件（文档/压缩包等）"
          >
            <Paperclip className="size-3.5" />
            附件
          </Button>

          <Button
            type="button"
            variant="outline"
            size="xs"
            onClick={() => insertAtCursor("$$ E = mc^2 $$")}
            className="gap-1 text-xs"
            title="插入 LaTeX 公式（行内 $..$ 或块级 $$..$$）"
          >
            <Sigma className="size-3.5" />
            LaTeX
          </Button>
        </div>
      </div>

      <TabsContent value="write" className="mt-0">
        <Textarea
          id={id}
          ref={textareaRef}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onPaste={handlePaste}
          onDrop={handleDrop}
          placeholder={placeholder ?? "支持 Markdown 与 LaTeX（$..$ 或 $$..$$），@用户名 可提醒对方，支持直接粘贴或拖拽图片"}
          rows={rows}
          className="font-mono text-sm"
          required
        />
      </TabsContent>
      <TabsContent value="preview" className="mt-0">
        <div className="min-h-24 rounded-lg border bg-muted/30 p-4">
          {value.trim() ? <MarkdownBody content={value} /> : <p className="text-sm text-muted-foreground">暂无内容</p>}
        </div>
      </TabsContent>
    </Tabs>
  )
}
