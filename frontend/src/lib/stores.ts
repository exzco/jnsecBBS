// 客户端 UI 偏好：nanostores + localStorage 持久化。
// 服务端数据（会话/列表/详情）仍由 react-query 管理，这里只放跨组件的界面偏好。
import { atom } from "nanostores"
import { useStore } from "@nanostores/react"

const HOME_BOARD_KEY = "jnsec:home-board"
const HOME_VIEW_KEY = "jnsec:home-view"

// 首页左侧选中的板块 slug；"all" 表示全部。
export const $homeBoard = atom<string>(localStorage.getItem(HOME_BOARD_KEY) ?? "all")

export function setHomeBoard(slug: string) {
  $homeBoard.set(slug)
  localStorage.setItem(HOME_BOARD_KEY, slug)
}

export function useHomeBoard(): [string, (slug: string) => void] {
  return [useStore($homeBoard), setHomeBoard]
}

// 首页主栏展示视图："boards"（版块总览矩阵）或 "feed"（讨论流列表）
export const $homeView = atom<"feed" | "boards">(
  (localStorage.getItem(HOME_VIEW_KEY) as "feed" | "boards") ?? "boards",
)

export function setHomeView(view: "feed" | "boards") {
  $homeView.set(view)
  localStorage.setItem(HOME_VIEW_KEY, view)
}

export function useHomeView(): ["feed" | "boards", (view: "feed" | "boards") => void] {
  return [useStore($homeView), setHomeView]
}
