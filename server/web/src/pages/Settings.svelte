<script lang="ts">
  import { api, type Status, type Settings, type Project, type Delivery, type Silence, type SilenceField } from '../lib/api'
  import { relative, duration, retentionLabel, compact } from '../lib/format'
  import Card from '../lib/ui/Card.svelte'
  import Button from '../lib/ui/Button.svelte'
  import Select from '../lib/ui/Select.svelte'
  import Input from '../lib/ui/Input.svelte'
  import Switch from '../lib/ui/Switch.svelte'
  import SettingRow from '../lib/ui/SettingRow.svelte'
  import StatusDot from '../lib/ui/StatusDot.svelte'
  import Notice from '../lib/ui/Notice.svelte'
  import CodeBlock from '../lib/ui/CodeBlock.svelte'
  import Metric from '../lib/ui/Metric.svelte'
  import { panel, pop, reorder, soft } from '../lib/motion'
  import Skeleton from '../lib/ui/Skeleton.svelte'
  import WebPushControl from '../lib/ui/WebPushControl.svelte'
  import { link } from '../lib/router.svelte'

  let status = $state<Status | null>(null)
  let settings = $state<Settings | null>(null)
  let projects = $state<Project[]>([])
  let error = $state('')
  let testing = $state(false)
  let testResult = $state<{ deliveries: Delivery[]; apns_configured: boolean; web_push_configured: boolean } | null>(null)
  let newKey = $state('')
  let testProject = $state('')
  let silences = $state<Silence[]>([])
  let silencedEvents = $state(0)
  let silField = $state<SilenceField>('title')
  let silValue = $state('')
  let silProject = $state('')
  let silNote = $state('')

  async function loadSilences() {
    try {
      const r = await api.silences()
      silences = r.silences
      silencedEvents = r.silenced_events
    } catch (e: any) {
      error = e.message
    }
  }

  async function addSilence() {
    if (!silValue.trim()) return
    try {
      await api.createSilence({ field: silField, value: silValue.trim(), project_id: silProject || undefined, note: silNote.trim() || undefined })
      silValue = ''
      silNote = ''
      await loadSilences()
    } catch (e: any) {
      error = e.message
    }
  }

  async function removeSilence(s: Silence) {
    try {
      await api.deleteSilence(s.id)
      silences = silences.filter((x) => x.id !== s.id)
    } catch (e: any) {
      error = e.message
    }
  }

  async function load() {
    try {
      ;[status, settings] = await Promise.all([api.status(), api.settings()])
      projects = (await api.projects()).projects
      await loadSilences()
    } catch (e: any) {
      error = e.message
    }
  }
  $effect(() => {
    load()
  })

  async function setMCP(v: boolean) {
    try {
      settings = await api.updateSettings({ mcp_enabled: v })
    } catch (e: any) {
      error = e.message
    }
  }

  async function setRetention(v: string) {
    try {
      settings = await api.updateSettings({ retention_days: Number(v) })
    } catch (e: any) {
      error = e.message
    }
  }

  async function addKey() {
    const k = newKey.trim()
    if (!k || !settings) return
    try {
      settings = await api.updateSettings({ redact_keys: [...settings.redact_keys, k] })
      newKey = ''
    } catch (e: any) {
      error = e.message
    }
  }

  async function removeKey(k: string) {
    if (!settings) return
    try {
      settings = await api.updateSettings({ redact_keys: settings.redact_keys.filter((x) => x !== k) })
    } catch (e: any) {
      error = e.message
    }
  }

  async function sendTest() {
    testing = true
    error = ''
    testResult = null
    try {
      testResult = await api.test(testProject || undefined)
      await load()
    } catch (e: any) {
      error = e.message
    } finally {
      testing = false
    }
  }

  const retentionOptions = [
    { value: '7', label: '7 days' },
    { value: '14', label: '14 days' },
    { value: '30', label: '30 days' },
    { value: '90', label: '90 days' },
    { value: '0', label: 'Unlimited' },
  ]
  const lastPush = $derived(status?.last_push ?? null)
  const apnsTestDeliveries = $derived(testResult?.deliveries.filter((delivery) => delivery.transport !== 'web_push') ?? [])
  const webPushTestDeliveries = $derived(testResult?.deliveries.filter((delivery) => delivery.transport === 'web_push') ?? [])
  const origin = typeof location !== 'undefined' ? location.origin : ''
</script>

<div class="stack">
  {#if error}<div transition:panel><Notice tone="bad">{error}</Notice></div>{/if}

  {#if !status && !error}
    <div class="metrics">
      {#each [0, 1, 2, 3] as i (i)}
        <Card><Skeleton lines={2} height={12} widths={['50%', '35%']} /></Card>
      {/each}
    </div>
    <Card><Skeleton lines={1} height={13} width="20%" /><div class="status" style="margin-top: 16px">{#each [0, 1, 2, 3, 4, 5] as i (i)}<Skeleton lines={2} height={11} widths={['40%', '70%']} />{/each}</div></Card>
    <Card><Skeleton lines={3} height={12} widths={['35%', '80%', '60%']} /></Card>
  {/if}

  {#if status}
    <div class="metrics" in:soft>
      <Card><Metric label="Events" value={compact(status.events)} /></Card>
      <Card><Metric label="Projects" value={String(status.projects)} /></Card>
      <Card><Metric label="Devices" value={String(status.devices)} delta={status.pushable_devices < status.devices ? `${status.pushable_devices} with push` : undefined} tone="neutral" /></Card>
      <Card><Metric label="Uptime" value={duration(status.uptime_seconds)} /></Card>
    </div>

    <Card title="Status">
      <div class="status">
        <div><span class="k">Server</span><StatusDot tone="ok">Healthy</StatusDot></div>
        <div><span class="k">Database</span>{#if status.database === 'ok'}<StatusDot tone="ok">Healthy</StatusDot>{:else}<StatusDot tone="bad">{status.database}</StatusDot>{/if}</div>
        <div>
          <span class="k">APNs</span>
          {#if status.apns.configured}
            <StatusDot tone="ok">Configured · {status.apns.environment}</StatusDot>
          {:else}
            <StatusDot tone="warn">Not configured</StatusDot>
          {/if}
        </div>
        <div>
          <span class="k">Web Push</span>
          {#if status.web_push.configured}
            <StatusDot tone={status.web_push.subscriptions > 0 ? 'ok' : 'muted'}>{status.web_push.subscriptions} active subscription{status.web_push.subscriptions === 1 ? '' : 's'}</StatusDot>
          {:else}
            <StatusDot tone="bad">Unavailable</StatusDot>
          {/if}
        </div>
        <div>
          <span class="k">Last push</span>
          {#if lastPush}
            <StatusDot tone={lastPush.status === 'sent' ? 'ok' : lastPush.status === 'failed' ? 'bad' : 'muted'}>
              {lastPush.status === 'sent' ? 'Successful' : lastPush.status === 'failed' ? 'Failed' : 'Skipped'} {relative(lastPush.attempted_at)}
            </StatusDot>
          {:else}
            <span class="muted">None yet</span>
          {/if}
        </div>
        <div><span class="k">Version</span><span>{status.version}</span></div>
        <div><span class="k">Database path</span><span class="mono">{status.database_path}</span></div>
        <div><span class="k">Base URL</span><span class="mono">{status.base_url}</span></div>
        <div><span class="k">Retention</span><span>{retentionLabel(status.retention_days)}</span></div>
        <div>
          <span class="k">Admin login</span>
          {#if status.admin_auth}<StatusDot tone="ok">Enabled</StatusDot>{:else}<StatusDot tone="warn">Off · set BOOP_ADMIN_USER and BOOP_ADMIN_PASSWORD</StatusDot>{/if}
        </div>
      </div>
      {#if !status.apns.configured}
        <div style="margin-top: 16px">
          <Notice tone="warn">
            Pushes are stored but not sent. {status.apns.error ? status.apns.error + '.' : ''} Set the APNS_* environment variables and restart Boop.
          </Notice>
        </div>
      {:else if lastPush?.status === 'failed' && lastPush.error}
        <div style="margin-top: 16px"><Notice tone="bad">Last push failed: {lastPush.error}</Notice></div>
      {/if}
      {#if status.web_push.subscriptions > 0}
        <div style="margin-top: 16px"><Notice tone="good">Web Push is active for {status.web_push.subscriptions} browser subscription{status.web_push.subscriptions === 1 ? '' : 's'}.</Notice></div>
      {/if}
    </Card>

    <Card title="Apple Push Notifications">
      <div class="status">
        <div><span class="k">Team id</span><span class="mono">{status.apns.team_id || '—'}</span></div>
        <div><span class="k">Key id</span><span class="mono">{status.apns.key_id || '—'}</span></div>
        <div><span class="k">Bundle id</span><span class="mono">{status.apns.bundle_id || '—'}</span></div>
        <div><span class="k">Environment</span><span>{status.apns.environment}</span></div>
      </div>
      {#if !status.apns.configured}
        <p class="secondary lead" style="margin-top: 16px">Add these to your container environment. The private key should be mounted as a file, not pasted into an environment variable.</p>
        <CodeBlock code={`APNS_TEAM_ID=YOUR_TEAM_ID\nAPNS_KEY_ID=YOUR_KEY_ID\nAPNS_BUNDLE_ID=com.example.Boop\nAPNS_PRIVATE_KEY_PATH=/run/secrets/apns.p8\nAPNS_ENVIRONMENT=production`} />
      {/if}
    </Card>

    <Card title="Web Push">
      <p class="secondary lead">Install Boop on your Home Screen and receive iPhone system notifications without an Apple Developer account. The VAPID identity is generated once and backed up with this SQLite database.</p>
      <div style="margin-top: 16px"><WebPushControl onchange={load} /></div>
    </Card>

    <Card title="Test Boop">
      <p class="secondary lead">Creates a test event and pushes it to every paired phone.</p>
      <div class="row" style="margin-top: 12px; flex-wrap: wrap">
        <Select bind:value={testProject} options={[{ value: '', label: projects.length ? `Project: ${projects[projects.length - 1]?.name}` : 'No project yet' }, ...projects.map((p) => ({ value: p.id, label: p.name }))]} style="width: 220px" aria-label="Project" />
        <Button onclick={sendTest} disabled={testing || projects.length === 0}>{testing ? 'Sending' : 'Send test notification'}</Button>
      </div>
      {#if testResult}
        <div style="margin-top: 16px" transition:panel>
          {#if apnsTestDeliveries.length === 0}
            <Notice tone="info">Event created. No paired phones with push registered, so nothing was sent.</Notice>
          {:else if !testResult.apns_configured}
            <Notice tone="warn">Event created, but APNs is not configured so {apnsTestDeliveries.length} delivery{apnsTestDeliveries.length === 1 ? ' was' : 'ies were'} skipped.</Notice>
          {:else if apnsTestDeliveries.every((d) => d.status === 'sent')}
            <Notice tone="good">Sent to {apnsTestDeliveries.length} device{apnsTestDeliveries.length === 1 ? '' : 's'}.</Notice>
          {:else}
            <Notice tone="bad">
              {#each apnsTestDeliveries.filter((d) => d.status !== 'sent') as d (d.id)}
                <div>{d.device_name}: {d.error}</div>
              {/each}
            </Notice>
          {/if}
          {#if webPushTestDeliveries.length > 0}
            <div style="margin-top: 8px">
              {#if webPushTestDeliveries.every((delivery) => delivery.status === 'sent')}
                <Notice tone="good">Web Push sent to {webPushTestDeliveries.length} browser subscription{webPushTestDeliveries.length === 1 ? '' : 's'}.</Notice>
              {:else}
                <Notice tone="bad">
                  {#each webPushTestDeliveries.filter((delivery) => delivery.status !== 'sent') as delivery (delivery.id)}
                    <div>{delivery.device_name}: {delivery.error}</div>
                  {/each}
                </Notice>
              {/if}
            </div>
          {/if}
        </div>
      {/if}
    </Card>
  {/if}

  {#if settings}
    <Card title="Retention">
      <SettingRow label="Keep events for" hint="Older events are deleted automatically once an hour. Unlimited keeps everything. If BOOP_RETENTION_DAYS is set in the environment it overrides this on restart.">
        <Select value={String(settings.retention_days)} options={retentionOptions.some((o) => o.value === String(settings!.retention_days)) ? retentionOptions : [...retentionOptions, { value: String(settings.retention_days), label: `${settings.retention_days} days` }]} onchange={(e) => setRetention((e.currentTarget as HTMLSelectElement).value)} style="width: 150px" />
      </SettingRow>
    </Card>

    <Card title="MCP">
      <p class="secondary lead">Lets the AI assistant you already use read your events over the Model Context Protocol (read-only: list, search and inspect events and projects). Endpoint: <span class="mono">{status?.base_url ?? origin}/mcp</span>.</p>
      <SettingRow label="MCP endpoint" hint={settings.mcp_token_set ? 'Connect with the BOOP_MCP_TOKEN bearer token, a device credential, or your admin login.' : 'No BOOP_MCP_TOKEN is set: device credentials and the admin login work; set the env var to give an agent its own token.'}>
        <Switch checked={settings.mcp_enabled} onchange={setMCP} label="MCP endpoint" />
      </SettingRow>
    </Card>

    <Card title="Silences">
      <p class="secondary lead">Events matching a rule are still stored and shown, but never pushed to a phone. Add rules from an event's page or here. Fingerprint and source match exactly; title ignores case.</p>
      <p class="lead" style="margin-top: 8px"><a href="/?silenced=true" onclick={link}>{silencedEvents} silenced event{silencedEvents === 1 ? '' : 's'}</a> · open one to unsilence it or push it now.</p>
      {#if silences.length === 0}
        <p class="muted" style="margin-top: 12px">No silences.</p>
      {:else}
        <div class="sils" style="margin-top: 12px">
          {#each silences as s (s.id)}
            <div class="sil" animate:reorder out:soft>
              <div class="sil-main">
                <span class="pill custom">{s.field}</span>
                <span class="mono sv">{s.value}</span>
                <span class="muted caption">· {s.project_name || 'every project'}{s.note ? ` · ${s.note}` : ''} · {relative(s.created_at)}</span>
              </div>
              <Button variant="danger" size="sm" onclick={() => removeSilence(s)}>Remove</Button>
            </div>
          {/each}
        </div>
      {/if}
      <form
        class="sil-form"
        onsubmit={(e) => {
          e.preventDefault()
          addSilence()
        }}
      >
        <Select bind:value={silField} options={[{ value: 'title', label: 'Title' }, { value: 'fingerprint', label: 'Fingerprint' }, { value: 'source', label: 'Source' }]} style="width: 130px" aria-label="Field" />
        <Input bind:value={silValue} placeholder="Value to match" aria-label="Value" mono />
        <Select bind:value={silProject} options={[{ value: '', label: 'Every project' }, ...projects.map((p) => ({ value: p.id, label: p.name }))]} style="width: 160px" aria-label="Project" />
        <Input bind:value={silNote} placeholder="Note (optional)" aria-label="Note" style="width: 180px" />
        <Button variant="secondary" type="submit" disabled={!silValue.trim()}>Add</Button>
      </form>
    </Card>

    <Card title="Redaction">
      <p class="secondary lead">Values under these keys are replaced with [REDACTED] anywhere in event data before it is stored. Matching ignores case and treats - and _ the same.</p>
      <div class="keys" style="margin-top: 12px">
        {#each settings.default_redact_keys as k (k)}
          <span class="pill muted">{k}</span>
        {/each}
        {#each settings.redact_keys as k (k)}
          <span class="pill custom" in:pop out:soft animate:reorder>{k}<button type="button" aria-label="Remove {k}" onclick={() => removeKey(k)}>×</button></span>
        {/each}
      </div>
      <form
        class="row"
        style="margin-top: 16px"
        onsubmit={(e) => {
          e.preventDefault()
          addKey()
        }}
      >
        <Input bind:value={newKey} placeholder="Add a key, e.g. ssn" aria-label="Redaction key" style="width: 220px" mono />
        <Button variant="secondary" type="submit" disabled={!newKey.trim()}>Add</Button>
      </form>
    </Card>
  {/if}
</div>

<style>
  .metrics { display: grid; grid-template-columns: repeat(4, 1fr); gap: var(--up-space-4); }
  .status { display: grid; grid-template-columns: 1fr 1fr; gap: 12px 24px; }
  .status > div { display: flex; flex-direction: column; gap: 3px; font: var(--up-type-meta); min-width: 0; }
  .status span:last-child { overflow: hidden; text-overflow: ellipsis; }
  .k { font: var(--up-type-caption); color: var(--up-text-muted); }
  .lead { font: var(--up-type-meta); line-height: 1.6; }
  .keys { display: flex; flex-wrap: wrap; gap: 8px; }
  .sils { display: flex; flex-direction: column; gap: 6px; }
  .sil { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 8px 10px; border-radius: var(--up-radius-control); transition: background 120ms ease-out; }
  .sil:hover { background: var(--up-bg-hover); }
  .sil-main { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; min-width: 0; font: var(--up-type-meta); }
  .sv { word-break: break-all; }
  .sil-form { display: flex; gap: 8px; margin-top: 16px; flex-wrap: wrap; align-items: center; }
  .sil-form :global(input:nth-of-type(1)) { flex: 1; min-width: 160px; }
  .pill { display: inline-flex; align-items: center; gap: 6px; font: var(--up-type-code); padding: 4px 10px; border-radius: var(--up-radius-pill); background: var(--up-bg-hover); box-shadow: var(--up-ring-inset); }
  .pill.custom { background: var(--up-accent-tint); color: var(--up-accent-hover); }
  .pill button { background: none; border: none; cursor: pointer; color: inherit; font-size: 14px; line-height: 1; padding: 0; }
  @media (max-width: 600px) {
    .metrics { grid-template-columns: 1fr 1fr; }
    .status { grid-template-columns: 1fr; }
  }
</style>
