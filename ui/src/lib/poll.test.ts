// poll() 单测：立即首拍、hidden 跳过、间隔触发、切回可见立即补拍、清理函数停表。
// document 以 stubGlobal 注入（node 环境无 DOM），定时器用 fake timers。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { poll } from './poll';

type DocStub = {
	hidden: boolean;
	listeners: Map<string, () => void>;
	addEventListener: (type: string, fn: () => void) => void;
	removeEventListener: (type: string, fn: () => void) => void;
};

function makeDoc(hidden: boolean): DocStub {
	const listeners = new Map<string, () => void>();
	return {
		hidden,
		listeners,
		addEventListener: (type, fn) => listeners.set(type, fn),
		removeEventListener: (type, fn) => {
			if (listeners.get(type) === fn) listeners.delete(type);
		}
	};
}

beforeEach(() => {
	vi.useFakeTimers();
	vi.stubGlobal('document', makeDoc(false));
});

afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

function fireVisibilityChange(doc: DocStub) {
	doc.listeners.get('visibilitychange')?.();
}

describe('poll()', () => {
	it('启动时立即执行一次，不等待第一个间隔', async () => {
		const fn = vi.fn();
		poll(fn, 10_000);
		await vi.advanceTimersByTimeAsync(0);
		expect(fn).toHaveBeenCalledTimes(1);
	});

	it('document.hidden 时不执行（含首拍）', async () => {
		vi.stubGlobal('document', makeDoc(true));
		const fn = vi.fn();
		poll(fn, 10_000);
		await vi.advanceTimersByTimeAsync(0);
		await vi.advanceTimersByTimeAsync(30_000);
		expect(fn).not.toHaveBeenCalled();
	});

	it('按间隔重复执行；hidden 恢复后继续', async () => {
		const fn = vi.fn();
		const doc = makeDoc(false);
		vi.stubGlobal('document', doc);
		poll(fn, 10_000);
		await vi.advanceTimersByTimeAsync(0);
		expect(fn).toHaveBeenCalledTimes(1);

		await vi.advanceTimersByTimeAsync(10_000);
		expect(fn).toHaveBeenCalledTimes(2);

		// hidden 的那拍跳过
		doc.hidden = true;
		await vi.advanceTimersByTimeAsync(10_000);
		expect(fn).toHaveBeenCalledTimes(2);

		// 恢复可见后下一拍继续
		doc.hidden = false;
		await vi.advanceTimersByTimeAsync(10_000);
		expect(fn).toHaveBeenCalledTimes(3);
	});

	it('hidden 期间切回可见时立即补拍，不等下一个间隔', async () => {
		const fn = vi.fn();
		const doc = makeDoc(false);
		vi.stubGlobal('document', doc);
		poll(fn, 10_000);
		await vi.advanceTimersByTimeAsync(0);
		expect(fn).toHaveBeenCalledTimes(1);

		doc.hidden = true;
		await vi.advanceTimersByTimeAsync(30_000);
		expect(fn).toHaveBeenCalledTimes(1); // hidden 期间不拍

		// 仍在 hidden 时触发事件：不拍
		fireVisibilityChange(doc);
		expect(fn).toHaveBeenCalledTimes(1);

		// 变可见：立即补拍
		doc.hidden = false;
		fireVisibilityChange(doc);
		expect(fn).toHaveBeenCalledTimes(2);
	});

	it('返回的清理函数停止轮询并移除 visibilitychange 监听', async () => {
		const fn = vi.fn();
		const doc = makeDoc(false);
		vi.stubGlobal('document', doc);
		const stop = poll(fn, 10_000);
		await vi.advanceTimersByTimeAsync(0);
		expect(fn).toHaveBeenCalledTimes(1);

		stop();
		await vi.advanceTimersByTimeAsync(60_000);
		expect(fn).toHaveBeenCalledTimes(1); // 不再增加

		fireVisibilityChange(doc);
		expect(fn).toHaveBeenCalledTimes(1); // 监听已移除
	});
});
