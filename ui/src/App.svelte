<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from './lib/api.js';
  import { roleLevel, toast } from './lib/ui.js';
  import Overlays from './lib/Overlays.svelte';
  import Dashboard from './lib/pages/Dashboard.svelte';
  import Sites from './lib/pages/Sites.svelte';
  import Ops from './lib/pages/Ops.svelte';
  import Certs from './lib/pages/Certs.svelte';
  import Tokens from './lib/pages/Tokens.svelte';
  import Audit from './lib/pages/Audit.svelte';

  let whoami = null;
  let agents = [];
  let page = 'dashboard';
  let pageRef;
  let timer;

  const isAdmin = () => roleLevel(whoami?.role) >= 3;
  const isOperator = () => roleLevel(whoami?.role) >= 2;

  const NAV = [
    { group: '概览' },
    { id: 'dashboard', label: '仪表盘', icon: 'M2.25 12l8.954-8.955a1.5 1.5 0 012.122 0L22.5 12M4.5 9.75v10.125c0 .621.504 1.125 1.125 1.125H9.75v-4.875c0-.621.504-1.125 1.125-1.125h2.25c.621 0 1.125.504 1.125 1.125V21h4.125c.621 0 1.125-.504 1.125-1.125V9.75' },
    { group: '站点管理' },
    { id: 'sites', label: '站点与服务', icon: 'M21.75 17.25v-.228a4.5 4.5 0 00-.12-1.03l-2.268-9.64a3.375 3.375 0 00-3.285-2.602H7.923a3.375 3.375 0 00-3.285 2.602l-2.268 9.64a4.5 4.5 0 00-.12 1.03v.228m19.5 0a3 3 0 01-3 3H5.25a3 3 0 01-3-3m19.5 0a3 3 0 00-3-3H5.25a3 3 0 00-3 3m16.5 0h.008v.008h-.008v-.008zm-3 0h.008v.008h-.008v-.008z' },
    { id: 'ops', label: '命令与发布', icon: 'M6.75 7.5l3 2.25-3 2.25m4.5 0h3m-9 8.25h13.5A2.25 2.25 0 0021 18V6a2.25 2.25 0 00-2.25-2.25H5.25A2.25 2.25 0 003 6v12a2.25 2.25 0 002.25 2.25z' },
    { group: '安全与审计', adminOnly: true },
    { id: 'certs', label: '设备证书', icon: 'M9 12.75L11.25 15 15 9.75m-3-7.036A11.959 11.959 0 013.598 6 11.99 11.99 0 003 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751A11.959 11.959 0 0112 2.714z' },
    { id: 'tokens', label: 'Agent 令牌', icon: 'M15.75 5.25a3 3 0 013 3m3 0a6 6 0 01-7.029 5.912c-.563-.097-1.159.026-1.563.43L10.5 17.25H8.25v2.25H6v2.25H2.25v-2.818c0-.597.237-1.17.659-1.591l6.499-6.499c.404-.404.527-1 .43-1.563A6 6 0 1121.75 8.25z' },
    { id: 'audit', label: '审计日志', icon: 'M12 6v6h4.5m4.5 0a9 9 0 11-18 0 9 9 0 0118 0z' },
  ];

  function visible(item) {
    if (item.group && item.adminOnly && !isAdmin()) return false;
    if ((item.id === 'tokens' || item.id === 'audit') && !isAdmin()) return false;
    return true;
  }

  async function load() {
    try {
      whoami = await api('/api/portal/whoami');
      const a = await api('/api/portal/agents');
      agents = a.agents ?? [];
    } catch (e) {
      toast('加载失败：' + e.message, 'err');
    }
  }

  onMount(() => {
    load();
    timer = setInterval(() => { if (!document.hidden) load(); }, 15000);
  });
  onDestroy(() => clearInterval(timer));
</script>

<div class="layout">
  <aside class="sidebar">
    <div class="brand">
      <div class="logo">OP</div>
      <div><b>Ops Portal</b><small>ops.yinghuoren.com</small></div>
    </div>
    <nav class="nav">
      {#each NAV as item}
        {#if visible(item)}
          {#if item.group}
            <div class="group">{item.group}</div>
          {:else}
            <button class="nav-item" class:active={page === item.id} onclick={() => (page = item.id)}>
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d={item.icon} /></svg>
              {item.label}
            </button>
          {/if}
        {/if}
      {/each}
    </nav>
    {#if whoami}
      <div class="me">
        <b>{whoami.username}</b>
        <span>{whoami.role}{whoami.dev_no_cert ? ' · dev' : ''}</span>
      </div>
    {/if}
  </aside>

  <main class="main">
    {#if page === 'dashboard'}
      <Dashboard {agents} {whoami} bind:this={pageRef} />
    {:else if page === 'sites'}
      <Sites {agents} {whoami} bind:this={pageRef} onChanged={load} />
    {:else if page === 'ops'}
      <Ops {agents} {whoami} bind:this={pageRef} onChanged={load} />
    {:else if page === 'certs'}
      <Certs {whoami} bind:this={pageRef} />
    {:else if page === 'tokens'}
      <Tokens {whoami} bind:this={pageRef} />
    {:else if page === 'audit'}
      <Audit {whoami} bind:this={pageRef} />
    {/if}
  </main>
</div>

<Overlays />
