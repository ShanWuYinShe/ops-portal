// toast 与确认弹窗（runes），由 Overlays.svelte 渲染。

export interface Toast {
	id: number;
	message: string;
	kind: 'ok' | 'err';
}

export const toasts = $state<{ list: Toast[] }>({ list: [] });
let seq = 0;

export function toast(message: string, kind: 'ok' | 'err' = 'ok', ms = 3600) {
	const id = ++seq;
	toasts.list.push({ id, message, kind });
	setTimeout(() => {
		const i = toasts.list.findIndex((t) => t.id === id);
		if (i >= 0) toasts.list.splice(i, 1);
	}, ms);
}

interface ConfirmAsk {
	title: string;
	message: string;
	danger: boolean;
}

export const confirmBox = $state<{ current: ConfirmAsk | null }>({ current: null });
let confirmResolve: ((v: boolean) => void) | null = null;

export function askConfirm(title: string, message: string, danger = false): Promise<boolean> {
	return new Promise((resolve) => {
		// 已有弹窗未处理时先按取消结算，避免上一个调用方的 Promise 永久悬挂。
		confirmResolve?.(false);
		confirmResolve = resolve;
		confirmBox.current = { title, message, danger };
	});
}

export function settleConfirm(result: boolean) {
	confirmBox.current = null;
	confirmResolve?.(result);
	confirmResolve = null;
}
