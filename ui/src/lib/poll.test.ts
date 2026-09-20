// poll() 单测：立即首拍、hidden 跳过、间隔触发、清理函数停表。
// document 以 stubGlobal 注入（node 环境无 DOM），定时器用 fake timers。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { poll } from './poll';

beforeEach(() => {
	vi.useFakeTimers();
	vi.stubGlobal('document', { hidden: false });
});

afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

describe('poll()', () => {
	it('启动时立即执行一次，不等待第一个间隔', async () => {
		const fn = vi.fn();
		poll(fn, 10_000);
		await vi.advanceTimersByTimeAsync(0);
		expect(fn).toHaveBeenCalledTimes(1);
	});

	it('document.hidden 时不执行（含首拍）', async () => {
		vi.stubGlobal('document', { hidden: true });
		const fn = vi.fn();
		poll(fn, 10_000);
		await vi.advanceTimersByTimeAsync(0);
		await vi.advanceTimersByTimeAsync(30_000);
		expect(fn).not.toHaveBeenCalled();
	});

	it('按间隔重复执行；hidden 恢复后继续', async () => {
		const fn = vi.fn();
		poll(fn, 10_000);
		await vi.advanceTimersByTimeAsync(0);
		expect(fn).toHaveBeenCalledTimes(1);

		await vi.advanceTimersByTimeAsync(10_000);
		expect(fn).toHaveBeenCalledTimes(2);

		// hidden 的那拍跳过
		(document as { hidden: boolean }).hidden = true;
		await vi.advanceTimersByTimeAsync(10_000);
		expect(fn).toHaveBeenCalledTimes(2);

		// 恢复可见后下一拍继续
		(document as { hidden: boolean }).hidden = false;
		await vi.advanceTimersByTimeAsync(10_000);
		expect(fn).toHaveBeenCalledTimes(3);
	});

	it('返回的清理函数停止轮询', async () => {
		const fn = vi.fn();
		const stop = poll(fn, 10_000);
		await vi.advanceTimersByTimeAsync(0);
		expect(fn).toHaveBeenCalledTimes(1);

		stop();
		await vi.advanceTimersByTimeAsync(60_000);
		expect(fn).toHaveBeenCalledTimes(1); // 不再增加
	});
});
