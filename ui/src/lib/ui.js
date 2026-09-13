// UI 工具：toast、confirm、相对时间、状态映射、轮询。

import { onDestroy } from "svelte";
import { writable } from "svelte/store";

/* ── toast ── */
export const toasts = writable([]);
let toastSeq = 0;

export function toast(message, kind = "ok", ms = 3600) {
  const id = ++toastSeq;
  toasts.update((l) => [...l, { id, message, kind }]);
  setTimeout(() => toasts.update((l) => l.filter((t) => t.id !== id)), ms);
}

/* ── confirm：返回 Promise<boolean>，由 Overlays.svelte 渲染 ── */
export const confirmState = writable(null);
let confirmResolve = null;

export function askConfirm(title, message, danger = false) {
  return new Promise((resolve) => {
    confirmResolve = resolve;
    confirmState.set({ title, message, danger });
  });
}

export function settleConfirm(result) {
  confirmState.set(null);
  if (confirmResolve) confirmResolve(result);
  confirmResolve = null;
}

/* ── 相对时间 ── */
export function relTime(iso) {
  if (!iso) return "—";
  const s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (s < 5) return "刚刚";
  if (s < 60) return `${Math.floor(s)} 秒前`;
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
  return `${Math.floor(s / 86400)} 天前`;
}

export function shortTime(iso) {
  if (!iso) return "—";
  const d = new Date(iso);
  const pad = (n) => String(n).padStart(2, "0");
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/* ── 状态 → 徽章样式 ── */
const BADGE = {
  running: "ok",
  healthy: "ok",
  done: "ok",
  active: "ok",
  ok: "ok",
  queued: "info",
  acked: "warn",
  pending: "warn",
  exited: "neutral",
  stopped: "neutral",
  revoked: "neutral",
  failed: "err",
  dead: "err",
  unhealthy: "err",
};

export function badgeOf(state) {
  const s = String(state || "").toLowerCase();
  for (const [k, v] of Object.entries(BADGE)) {
    if (s.includes(k)) return v;
  }
  return "neutral";
}

export function agentOnline(lastSeen) {
  return lastSeen && Date.now() - new Date(lastSeen).getTime() < 60_000;
}

/* ── 轮询：页面可见时每 interval 执行 fn；组件销毁时自动清理 ── */
export function startPolling(fn, intervalMs) {
  fn();
  const t = setInterval(() => {
    if (!document.hidden) fn();
  }, intervalMs);
  onDestroy(() => clearInterval(t));
  return () => clearInterval(t);
}

/* ── 角色等级（与后端 roleLevel 对齐）── */
export function roleLevel(role) {
  return { "ops.admin": 3, "ops.operator": 2, "ops.viewer": 1 }[role] ?? 0;
}
