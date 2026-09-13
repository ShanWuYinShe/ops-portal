<script>
  import { api } from '../api.js';
  import { askConfirm, toast, shortTime, roleLevel, startPolling } from '../ui.js';

  export let whoami = null;

  let certs = [];
  let showForm = false;
  let form = { label: '', kind: 'human', cf_id: '', serial: '', fingerprint: '', common_name: '', expires_at: '', note: '' };

  const isAdmin = () => roleLevel(whoami?.role) >= 3;

  $: soonExpiring = certs.filter((c) => {
    if (c.status !== 'active' || !c.expires_at) return false;
    return new Date(c.expires_at).getTime() - Date.now() < 30 * 86400_000;
  });

  async function load() {
    try {
      const r = await api('/api/portal/certs');
      certs = r.certs ?? [];
    } catch { /* 静默重试 */ }
  }

  async function register() {
    if (!form.label || !form.serial) { toast('label 与 serial 必填（CF 建证后回填）', 'err'); return; }
    try {
      await api('/api/portal/certs/register', { method: 'POST', body: JSON.stringify(form) });
      toast(`已登记 ${form.label}`);
      form = { label: '', kind: 'human', cf_id: '', serial: '', fingerprint: '', common_name: '', expires_at: '', note: '' };
      showForm = false;
      await load();
    } catch (e) {
      toast('登记失败：' + e.message, 'err');
    }
  }

  async function revoke(c) {
    if (!(await askConfirm('吊销证书', `吊销 ${c.label}（serial ${c.serial.slice(0, 16)}…）？\n本地吊销后请同步到 CF 面板/用 API 吊销，边缘才真正拒绝。`, true))) return;
    try {
      const r = await api('/api/portal/certs/revoke', { method: 'POST', body: JSON.stringify({ id: c.id }) });
      toast(r.cf_sync_required ? '本地已吊销，请同步吊销 CF 侧证书' : '已吊销', 'ok', 5200);
      await load();
    } catch (e) {
      toast('吊销失败：' + e.message, 'err');
    }
  }

  startPolling(load, 20000);
</script>

<div class="page-head">
  <div>
    <h1 style="font-size:19px">设备证书</h1>
    <div class="sub">CF 托管 CA 签发的设备证书登记表 · 门禁以本表为准</div>
  </div>
  {#if isAdmin()}
    <button class="btn primary" onclick={() => (showForm = !showForm)}>{showForm ? '收起' : '登记证书'}</button>
  {/if}
</div>

{#if showForm}
  <div class="card sect">
    <div class="card-head"><h2>登记新证书</h2></div>
    <div class="card-body">
      <div class="form-row">
        <div class="field"><label>Label（持有人-设备）*</label><input placeholder="liyou-iphone" bind:value={form.label} /></div>
        <div class="field" style="flex:0;min-width:110px"><label>类型</label>
          <select bind:value={form.kind}><option value="human">human</option><option value="agent">agent</option></select>
        </div>
        <div class="field"><label>Serial Number *</label><input placeholder="CF 返回的 serial_number" bind:value={form.serial} /></div>
      </div>
      <div class="form-row" style="margin-top:10px">
        <div class="field"><label>CF 证书 ID</label><input bind:value={form.cf_id} /></div>
        <div class="field" style="flex:2"><label>SHA-256 指纹</label><input bind:value={form.fingerprint} /></div>
        <div class="field"><label>有效期至</label><input placeholder="2028-12-16" bind:value={form.expires_at} /></div>
      </div>
      <div class="form-row" style="margin-top:10px">
        <div class="field"><label>Common Name</label><input bind:value={form.common_name} /></div>
        <div class="field" style="flex:2"><label>备注</label><input bind:value={form.note} /></div>
        <button class="btn primary" onclick={register}>保存</button>
      </div>
    </div>
  </div>
{/if}

{#if soonExpiring.length}
  <div class="card sect" style="border-color:var(--warn)">
    <div class="card-body" style="padding:11px 16px;color:var(--warn)">
      ⚠ {soonExpiring.length} 张证书 30 天内到期：{soonExpiring.map((c) => c.label).join('、')}，请及时重签。
    </div>
  </div>
{/if}

<div class="card sect">
  <table class="table">
    <thead><tr><th>Label</th><th>类型</th><th>Serial</th><th>状态</th><th>有效期至</th><th>登记时间</th><th></th></tr></thead>
    <tbody>
      {#each certs as c (c.id)}
        <tr>
          <td><b>{c.label}</b>{#if c.note}<div class="hint">{c.note}</div>{/if}</td>
          <td><span class="chip">{c.kind}</span></td>
          <td class="mono text-2" title={c.fingerprint}>{c.serial.slice(0, 18)}…</td>
          <td><span class="badge {c.status === 'active' ? 'ok' : 'neutral'}">{c.status}</span></td>
          <td class="text-2">{c.expires_at ? c.expires_at.slice(0, 10) : '—'}</td>
          <td class="mono text-2">{shortTime(c.created_at)}</td>
          <td class="td-actions">
            {#if isAdmin() && c.status === 'active'}
              <button class="btn sm danger" onclick={() => revoke(c)}>吊销</button>
            {/if}
          </td>
        </tr>
      {:else}
        <tr><td colspan="7"><div class="empty">暂无证书登记</div></td></tr>
      {/each}
    </tbody>
  </table>
</div>
