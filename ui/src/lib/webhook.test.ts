// webhook.ts 纯逻辑单测：接收 URL 拼接、查询串过滤、状态映射、字节/耗时展示。
// 网络请求（api()）不在此覆盖，由后端 handler 单测与端到端验证保障。
import { describe, expect, it } from 'vitest';
import {
	asBool,
	attemptBadgeKind,
	bytesText,
	durationText,
	inboundUrl,
	queryString,
	statusBadgeKind,
	statusLabel,
	DELIVERY_STATUSES
} from './webhook';

describe('inboundUrl()', () => {
	it('拼出 origin + /api/hooks/<slug>', () => {
		expect(inboundUrl('https://ops.yinghuoren.com', 'github-main')).toBe(
			'https://ops.yinghuoren.com/api/hooks/github-main'
		);
	});

	it('容忍 origin 尾部斜杠，不产生双斜杠', () => {
		expect(inboundUrl('https://ops.yinghuoren.com/', 'x')).toBe(
			'https://ops.yinghuoren.com/api/hooks/x'
		);
		expect(inboundUrl('https://ops.yinghuoren.com///', 'x')).toBe(
			'https://ops.yinghuoren.com/api/hooks/x'
		);
	});

	it('slug 里的特殊字符被转义，空格不落进路径', () => {
		expect(inboundUrl('https://a.test', 'a b/c')).toBe('https://a.test/api/hooks/a%20b%2Fc');
	});

	it('origin 或 slug 为空时仍产出合法形状（不抛错）', () => {
		expect(inboundUrl('', 'demo')).toBe('/api/hooks/demo');
		expect(inboundUrl('https://a.test', '')).toBe('https://a.test/api/hooks/');
	});
});

describe('queryString()', () => {
	it('丢弃 undefined / null / 空串', () => {
		expect(queryString({ limit: 50, status: '', endpoint_id: undefined })).toBe('?limit=50');
		expect(queryString({ status: null })).toBe('');
	});

	it('保留数值 0（不能被当成空值丢掉）', () => {
		expect(queryString({ offset: 0 })).toBe('?offset=0');
	});

	it('保留 false 的字符串形式并做 URL 编码', () => {
		expect(queryString({ a: 'x y', b: 'false' })).toBe('?a=x+y&b=false');
	});

	it('全空时返回空串（路径不带 ?）', () => {
		expect(queryString({})).toBe('');
	});
});

describe('statusLabel() / statusBadgeKind()', () => {
	it('可筛选状态每种都有中文标签与配色', () => {
		for (const s of DELIVERY_STATUSES) {
			expect(statusLabel(s)).not.toBe(s);
			expect(statusBadgeKind(s)).not.toBe('');
		}
	});

	it('筛选列表不含 dedup：该状态不会落库，选了只会看到空列表', () => {
		// 命中幂等时接收端点直接 200 {"dedup":true} 且不新建记录（docs/webhook.md），
		// 故 'dedup' 不是可筛选状态；此断言防止有人照搬后端白名单又把它加回来。
		expect(DELIVERY_STATUSES).not.toContain('dedup');
		expect([...DELIVERY_STATUSES]).toEqual(['pending', 'delivered', 'failed', 'rejected']);
	});

	it('具体映射符合约定', () => {
		expect(statusLabel('pending')).toBe('待转发');
		expect(statusLabel('delivered')).toBe('已送达');
		expect(statusLabel('failed')).toBe('转发失败');
		expect(statusLabel('rejected')).toBe('已拒绝');
		expect(statusLabel('dedup')).toBe('重复投递');

		expect(statusBadgeKind('delivered')).toBe('ok');
		expect(statusBadgeKind('pending')).toBe('info');
		expect(statusBadgeKind('dedup')).toBe('warn');
		expect(statusBadgeKind('failed')).toBe('err');
		expect(statusBadgeKind('rejected')).toBe('err');
	});

	it('未知/空状态不崩：原样展示、无配色', () => {
		expect(statusLabel('weird')).toBe('weird');
		expect(statusLabel('')).toBe('—');
		expect(statusBadgeKind('weird')).toBe('');
	});
});

describe('asBool()', () => {
	it('兼容 SQLite 的 0/1 与真实布尔', () => {
		expect(asBool(true)).toBe(true);
		expect(asBool(1)).toBe(true);
		expect(asBool(false)).toBe(false);
		expect(asBool(0)).toBe(false);
		expect(asBool(undefined)).toBe(false);
		expect(asBool(null)).toBe(false);
	});
});

describe('bytesText() / durationText() / attemptBadgeKind()', () => {
	it('字节可读化', () => {
		expect(bytesText(0)).toBe('0 B');
		expect(bytesText(-1)).toBe('0 B');
		expect(bytesText(512)).toBe('512 B');
		expect(bytesText(2048)).toBe('2.0 KB');
		expect(bytesText(3 * 1024 * 1024)).toBe('3.00 MB');
	});

	it('耗时展示', () => {
		expect(durationText(0)).toBe('—');
		expect(durationText(250)).toBe('250 ms');
		expect(durationText(1500)).toBe('1.50 s');
	});

	it('尝试状态配色：ok 为成功，其余为失败', () => {
		expect(attemptBadgeKind('ok')).toBe('ok');
		expect(attemptBadgeKind('failed')).toBe('err');
		expect(attemptBadgeKind('')).toBe('err');
	});
});
