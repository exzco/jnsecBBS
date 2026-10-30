import { useCallback, useEffect, useRef, useState } from "react"
import { Check, Loader2, RotateCcw, X, ZoomIn, ZoomOut } from "lucide-react"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"

interface AvatarCropperModalProps {
  open: boolean
  imageSrc: string | null
  isUploading?: boolean
  onClose: () => void
  onConfirm: (blob: Blob) => Promise<void> | void
}

interface CropRect {
  x: number
  y: number
  size: number
}

export function AvatarCropperModal({
  open,
  imageSrc,
  isUploading = false,
  onClose,
  onConfirm,
}: AvatarCropperModalProps) {
  const imageRef = useRef<HTMLImageElement>(null)
  const previewCanvasRef = useRef<HTMLCanvasElement>(null)

  const [imgLayout, setImgLayout] = useState<{
    width: number
    height: number
    naturalWidth: number
    naturalHeight: number
  } | null>(null)

  const [crop, setCrop] = useState<CropRect>({ x: 0, y: 0, size: 100 })
  const [dragMode, setDragMode] = useState<"move" | "nw" | "ne" | "sw" | "se" | null>(null)
  const dragStartRef = useRef<{
    pointerX: number
    pointerY: number
    initCrop: CropRect
  } | null>(null)

  // 基于图片原始宽高计算适合弹窗预览的最佳显示尺寸与居中选框
  const initLayoutAndCrop = useCallback(() => {
    const img = imageRef.current
    if (!img) return
    const nw = img.naturalWidth
    const nh = img.naturalHeight
    if (nw <= 0 || nh <= 0) return

    const maxW = 360
    const maxH = 280
    const scale = Math.min(maxW / nw, maxH / nh, 1)
    const w = Math.max(60, Math.round(nw * scale))
    const h = Math.max(60, Math.round(nh * scale))

    setImgLayout({
      width: w,
      height: h,
      naturalWidth: nw,
      naturalHeight: nh,
    })

    const initialSize = Math.max(40, Math.floor(Math.min(w, h) * 0.8))
    setCrop({
      x: Math.floor((w - initialSize) / 2),
      y: Math.floor((h - initialSize) / 2),
      size: initialSize,
    })
  }, [])

  // 弹窗打开或图片变更时触发尺寸初始化
  useEffect(() => {
    if (!open) {
      setImgLayout(null)
      setDragMode(null)
      return
    }
    const img = imageRef.current
    if (img && img.complete && img.naturalWidth > 0) {
      initLayoutAndCrop()
    }
  }, [open, imageSrc, initLayoutAndCrop])

  // 重置回居中默认选框
  const handleReset = () => {
    if (!imgLayout) return
    const initialSize = Math.max(40, Math.floor(Math.min(imgLayout.width, imgLayout.height) * 0.8))
    setCrop({
      x: Math.floor((imgLayout.width - initialSize) / 2),
      y: Math.floor((imgLayout.height - initialSize) / 2),
      size: initialSize,
    })
  }

  // 缩放滑块调节大小（保持中心位置）
  const handleSliderChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (!imgLayout) return
    const newSize = Number(e.target.value)
    const minSize = 40
    const maxSize = Math.min(imgLayout.width, imgLayout.height)
    const clampedSize = Math.min(Math.max(newSize, minSize), maxSize)

    const centerX = crop.x + crop.size / 2
    const centerY = crop.y + crop.size / 2

    let nextX = Math.round(centerX - clampedSize / 2)
    let nextY = Math.round(centerY - clampedSize / 2)

    nextX = Math.max(0, Math.min(nextX, imgLayout.width - clampedSize))
    nextY = Math.max(0, Math.min(nextY, imgLayout.height - clampedSize))

    setCrop({ x: nextX, y: nextY, size: clampedSize })
  }

  // 开始拖拽
  const startDrag = (
    mode: "move" | "nw" | "ne" | "sw" | "se",
    e: React.PointerEvent
  ) => {
    e.preventDefault()
    e.stopPropagation()
    setDragMode(mode)
    dragStartRef.current = {
      pointerX: e.clientX,
      pointerY: e.clientY,
      initCrop: { ...crop },
    }
  }

  // 全局 Pointer 移动与释放监听，避免边缘丢帧与拖拽卡死
  useEffect(() => {
    if (!dragMode || !imgLayout) return

    const handlePointerMove = (e: PointerEvent) => {
      if (!dragStartRef.current || !imgLayout) return
      const { pointerX, pointerY, initCrop } = dragStartRef.current
      const dx = e.clientX - pointerX
      const dy = e.clientY - pointerY
      const minSize = 40
      const maxSize = Math.min(imgLayout.width, imgLayout.height)

      if (dragMode === "move") {
        let nextX = initCrop.x + dx
        let nextY = initCrop.y + dy
        nextX = Math.max(0, Math.min(nextX, imgLayout.width - initCrop.size))
        nextY = Math.max(0, Math.min(nextY, imgLayout.height - initCrop.size))
        setCrop({ x: Math.round(nextX), y: Math.round(nextY), size: initCrop.size })
        return
      }

      // 维持 1:1 正方形比例拉伸
      let newSize = initCrop.size
      let nextX = initCrop.x
      let nextY = initCrop.y

      if (dragMode === "se") {
        const delta = Math.max(dx, dy)
        newSize = Math.max(minSize, Math.min(maxSize, initCrop.size + delta))
        newSize = Math.min(newSize, imgLayout.width - initCrop.x, imgLayout.height - initCrop.y)
      } else if (dragMode === "nw") {
        const delta = Math.max(-dx, -dy)
        newSize = Math.max(minSize, Math.min(maxSize, initCrop.size + delta))
        newSize = Math.min(newSize, initCrop.x + initCrop.size, initCrop.y + initCrop.size)
        nextX = initCrop.x + (initCrop.size - newSize)
        nextY = initCrop.y + (initCrop.size - newSize)
      } else if (dragMode === "ne") {
        const delta = Math.max(dx, -dy)
        newSize = Math.max(minSize, Math.min(maxSize, initCrop.size + delta))
        newSize = Math.min(newSize, imgLayout.width - initCrop.x, initCrop.y + initCrop.size)
        nextY = initCrop.y + (initCrop.size - newSize)
      } else if (dragMode === "sw") {
        const delta = Math.max(-dx, dy)
        newSize = Math.max(minSize, Math.min(maxSize, initCrop.size + delta))
        newSize = Math.min(newSize, initCrop.x + initCrop.size, imgLayout.height - initCrop.y)
        nextX = initCrop.x + (initCrop.size - newSize)
      }

      setCrop({
        x: Math.round(Math.max(0, nextX)),
        y: Math.round(Math.max(0, nextY)),
        size: Math.round(newSize),
      })
    }

    const handlePointerUp = () => {
      setDragMode(null)
      dragStartRef.current = null
    }

    window.addEventListener("pointermove", handlePointerMove)
    window.addEventListener("pointerup", handlePointerUp)
    window.addEventListener("pointercancel", handlePointerUp)
    return () => {
      window.removeEventListener("pointermove", handlePointerMove)
      window.removeEventListener("pointerup", handlePointerUp)
      window.removeEventListener("pointercancel", handlePointerUp)
    }
  }, [dragMode, imgLayout])

  // 实时更新圆形与方形预览画布
  const updatePreview = useCallback(() => {
    const canvas = previewCanvasRef.current
    const img = imageRef.current
    if (!canvas || !img || !imgLayout) return

    const ctx = canvas.getContext("2d")
    if (!ctx) return

    const scale = imgLayout.naturalWidth / imgLayout.width
    const sx = Math.max(0, crop.x * scale)
    const sy = Math.max(0, crop.y * scale)
    const sSize = Math.min(
      crop.size * scale,
      imgLayout.naturalWidth - sx,
      imgLayout.naturalHeight - sy
    )

    canvas.width = 160
    canvas.height = 160
    ctx.clearRect(0, 0, 160, 160)
    ctx.imageSmoothingEnabled = true
    ctx.imageSmoothingQuality = "high"
    ctx.drawImage(img, sx, sy, sSize, sSize, 0, 0, 160, 160)
  }, [crop, imgLayout])

  useEffect(() => {
    updatePreview()
  }, [updatePreview])

  // 生成最终高清正方形图片并提交
  const handleConfirm = () => {
    const img = imageRef.current
    if (!img || !imgLayout) return

    const canvas = document.createElement("canvas")
    const targetSize = 256
    canvas.width = targetSize
    canvas.height = targetSize

    const ctx = canvas.getContext("2d")
    if (!ctx) return

    const scale = imgLayout.naturalWidth / imgLayout.width
    const sx = Math.max(0, crop.x * scale)
    const sy = Math.max(0, crop.y * scale)
    const sSize = Math.min(
      crop.size * scale,
      imgLayout.naturalWidth - sx,
      imgLayout.naturalHeight - sy
    )

    ctx.imageSmoothingEnabled = true
    ctx.imageSmoothingQuality = "high"
    ctx.drawImage(img, sx, sy, sSize, sSize, 0, 0, targetSize, targetSize)

    canvas.toBlob(
      (blob) => {
        if (blob) {
          void onConfirm(blob)
        }
      },
      "image/png",
      0.95
    )
  }

  const minCropSize = 40
  const maxCropSize = imgLayout ? Math.min(imgLayout.width, imgLayout.height) : 200

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !isUploading && onClose()}>
      <DialogContent
        showCloseButton={!isUploading}
        className="max-w-[95vw] sm:max-w-xl md:max-w-2xl p-4 sm:p-6"
      >
        <DialogHeader>
          <DialogTitle className="text-lg font-bold">裁剪头像</DialogTitle>
          <DialogDescription className="text-xs sm:text-sm text-muted-foreground">
            拖动高亮框或调节四角滑块选择头像范围，支持通过滑块微调大小。
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col md:flex-row items-center gap-5 my-2">
          {/* 左侧主裁剪区 */}
          <div className="flex-1 w-full flex flex-col items-center">
            <div
              className="relative select-none overflow-hidden rounded-lg border border-border bg-black/95 shadow-inner flex items-center justify-center w-full max-w-[380px] h-[300px]"
              style={{ touchAction: "none" }}
            >
              {imgLayout ? (
                <div
                  className="relative select-none"
                  style={{
                    width: imgLayout.width,
                    height: imgLayout.height,
                  }}
                >
                  <img
                    ref={imageRef}
                    src={imageSrc ?? undefined}
                    alt="待裁剪头像"
                    onLoad={initLayoutAndCrop}
                    style={{
                      width: imgLayout.width,
                      height: imgLayout.height,
                      display: "block",
                    }}
                    className="pointer-events-none select-none"
                    draggable={false}
                  />

                  {/* 裁剪交互遮罩与选框 */}
                  <div className="absolute inset-0 pointer-events-none">
                    {/* 四周暗色遮罩 */}
                    <div
                      className="absolute left-0 top-0 w-full bg-black/60 pointer-events-none"
                      style={{ height: crop.y }}
                    />
                    <div
                      className="absolute left-0 w-full bg-black/60 pointer-events-none"
                      style={{
                        top: crop.y + crop.size,
                        bottom: 0,
                      }}
                    />
                    <div
                      className="absolute left-0 bg-black/60 pointer-events-none"
                      style={{
                        top: crop.y,
                        height: crop.size,
                        width: crop.x,
                      }}
                    />
                    <div
                      className="absolute right-0 bg-black/60 pointer-events-none"
                      style={{
                        top: crop.y,
                        height: crop.size,
                        left: crop.x + crop.size,
                      }}
                    />

                    {/* 选框本身：可拖拽移动 */}
                    <div
                      className="absolute pointer-events-auto cursor-move border-2 border-white shadow-[0_0_0_1px_rgba(0,0,0,0.5),0_0_15px_rgba(0,0,0,0.5)]"
                      style={{
                        left: crop.x,
                        top: crop.y,
                        width: crop.size,
                        height: crop.size,
                        touchAction: "none",
                      }}
                      onPointerDown={(e) => startDrag("move", e)}
                    >
                      {/* 九宫格参考线 */}
                      <div className="absolute inset-0 grid grid-cols-3 grid-rows-3 pointer-events-none opacity-40">
                        <div className="border-r border-b border-dashed border-white" />
                        <div className="border-r border-b border-dashed border-white" />
                        <div className="border-b border-dashed border-white" />
                        <div className="border-r border-b border-dashed border-white" />
                        <div className="border-r border-b border-dashed border-white" />
                        <div className="border-b border-dashed border-white" />
                        <div className="border-r border-dashed border-white" />
                        <div className="border-r border-dashed border-white" />
                        <div />
                      </div>

                      {/* 圆形虚线提示 */}
                      <div className="absolute inset-0 rounded-full border border-dashed border-primary pointer-events-none" />

                      {/* 四角缩放把手 */}
                      <div
                        className="absolute -top-2 -left-2 size-4 rounded-xs bg-white border-2 border-primary cursor-nwse-resize shadow-md"
                        style={{ touchAction: "none" }}
                        onPointerDown={(e) => startDrag("nw", e)}
                      />
                      <div
                        className="absolute -top-2 -right-2 size-4 rounded-xs bg-white border-2 border-primary cursor-nesw-resize shadow-md"
                        style={{ touchAction: "none" }}
                        onPointerDown={(e) => startDrag("ne", e)}
                      />
                      <div
                        className="absolute -bottom-2 -left-2 size-4 rounded-xs bg-white border-2 border-primary cursor-nesw-resize shadow-md"
                        style={{ touchAction: "none" }}
                        onPointerDown={(e) => startDrag("sw", e)}
                      />
                      <div
                        className="absolute -bottom-2 -right-2 size-4 rounded-xs bg-white border-2 border-primary cursor-nwse-resize shadow-md"
                        style={{ touchAction: "none" }}
                        onPointerDown={(e) => startDrag("se", e)}
                      />
                    </div>
                  </div>
                </div>
              ) : (
                <img
                  ref={imageRef}
                  src={imageSrc ?? undefined}
                  alt="待裁剪头像"
                  onLoad={initLayoutAndCrop}
                  className="max-h-[280px] max-w-[360px] object-contain opacity-0 pointer-events-none"
                />
              )}
            </div>

            {/* 底部缩放微调工具条 */}
            <div className="mt-3 flex w-full max-w-[380px] items-center gap-2 px-1">
              <ZoomOut className="size-4 shrink-0 text-muted-foreground" />
              <input
                type="range"
                min={minCropSize}
                max={maxCropSize}
                value={crop.size}
                onChange={handleSliderChange}
                disabled={!imgLayout || isUploading}
                aria-label="裁剪框大小"
                className="h-1.5 w-full cursor-pointer appearance-none rounded-lg bg-secondary accent-primary"
              />
              <ZoomIn className="size-4 shrink-0 text-muted-foreground" />
              <Button
                variant="ghost"
                size="icon-xs"
                title="重置选框"
                onClick={handleReset}
                disabled={!imgLayout || isUploading}
                className="ml-1 text-muted-foreground hover:text-foreground"
              >
                <RotateCcw className="size-3.5" />
              </Button>
            </div>
          </div>

          {/* 右侧实时效果预览 */}
          <div className="flex md:flex-col items-center justify-center gap-5 rounded-lg border border-border/60 bg-muted/30 p-4 w-full md:w-44 shrink-0">
            <div className="text-center">
              <p className="text-xs font-medium text-muted-foreground mb-2">圆形预览</p>
              <div className="size-20 rounded-full overflow-hidden border-2 border-primary/50 shadow-sm mx-auto bg-background">
                <canvas
                  ref={previewCanvasRef}
                  className="size-full object-cover rounded-full"
                />
              </div>
            </div>

            <div className="text-center">
              <p className="text-xs font-medium text-muted-foreground mb-2">方形预览</p>
              <div className="size-14 rounded-md overflow-hidden border border-border shadow-xs mx-auto bg-background">
                <canvas
                  ref={(node) => {
                    if (node && previewCanvasRef.current) {
                      const ctx = node.getContext("2d")
                      if (ctx) {
                        node.width = 56
                        node.height = 56
                        ctx.drawImage(previewCanvasRef.current, 0, 0, 56, 56)
                      }
                    }
                  }}
                  className="size-full object-cover"
                />
              </div>
            </div>
          </div>
        </div>

        <DialogFooter className="mt-2 flex-row justify-end gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={onClose}
            disabled={isUploading}
          >
            <X className="size-3.5 mr-1" /> 取消
          </Button>
          <Button
            size="sm"
            onClick={handleConfirm}
            disabled={!imgLayout || isUploading}
          >
            {isUploading ? (
              <>
                <Loader2 className="size-3.5 mr-1 animate-spin" /> 保存中…
              </>
            ) : (
              <>
                <Check className="size-3.5 mr-1" /> 确认并上传
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
