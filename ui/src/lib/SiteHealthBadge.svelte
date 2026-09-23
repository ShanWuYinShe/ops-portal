<script lang="ts">
	import type { SiteOverview } from '$lib/api';

	// 站点拨测健康徽标：portal 以 service 证书拨测 ops_url 的结论。
	// dashboard 一览与 sites 卡头共用，勿在页面里内联同款逻辑。
	let { site }: { site: SiteOverview } = $props();
</script>

{#if site.reachable === true && (!site.health || site.health === 'ok')}
	<span class="badge bg-ok-soft text-ok" title="portal 拨测 {site.ops_url}/ops/v1/health 通过">可达</span>
{:else if site.reachable === true && site.health === 'degraded'}
	<span class="badge bg-warn-soft text-warn" title="站点自报降级，就绪但有依赖故障">降级</span>
{:else if site.reachable === true && site.health === 'starting'}
	<span class="badge bg-warn-soft text-warn" title="站点自报启动中（迁移/预热），稍后自愈">启动中</span>
{:else if site.reachable === true}
	<span class="badge bg-err-soft text-err" title="站点自报不可服务（ok:false）">故障</span>
{:else if site.reachable === false}
	<span class="badge bg-err-soft text-err" title="portal 拨测失败，检查接口与放行清单">不可达</span>
{:else}
	<span
		class="badge"
		title={site.ops_url
			? '已注册 ops_url 但尚未拨测（等 service 证书签发后的下一轮）'
			: 'agent 发现的站点，未注册 ops_url，不参与拨测'}
	>
		{site.ops_url ? '未探测' : '未接入拨测'}
	</span>
{/if}
