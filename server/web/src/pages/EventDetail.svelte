<script lang="ts">
  import { api, type Event, type Delivery, type Silence } from '../lib/api'
  import { link, groupPath } from '../lib/router.svelte'
  import { fullDate, relative } from '../lib/format'
  import Card from '../lib/ui/Card.svelte'
  import LevelBadge from '../lib/ui/LevelBadge.svelte'
  import JsonTree from '../lib/ui/JsonTree.svelte'
  import CodeBlock from '../lib/ui/CodeBlock.svelte'
  import Notice from '../lib/ui/Notice.svelte'
  import Empty from '../lib/ui/Empty.svelte'
  import StatusDot from '../lib/ui/StatusDot.svelte'
  import ProjectIcon from '../lib/ui/ProjectIcon.svelte'
  import { panel, pop } from '../lib/motion'
  import Button from '../lib/ui/Button.svelte'
  import Skeleton from '../lib/ui/Skeleton.svelte'

  let { id }: { id: string } = $props()
  let event = $state<Event | null>(null)
  let deliveries = $state<Delivery[]>([])
  let error = $state('')
  let showRaw = $state(false)
  let silenceOpen = $state(false)
  let silenceDone = $state<string | null>(null)
  let silenceBusy = $state(false)
  let rule = $state<Silence | null>(null)

  async function loadRule(e: Event) {
    rule = null
    if (e.silence_id) rule = await api.silence(e.silence_id).catch(() => null)
  }

  async function removeRule() {
    if (!rule) return
    silenceBusy = true
    try {
      await api.deleteSilence(rule.id)
      silenceDone = `Removed the ${rule.field} rule. Future matches will be pushed again; this event stays marked as silenced.`
      rule = null
    } catch (e: any) {
      error = e.message
    } finally {
      silenceBusy = false
    }
  }

  async function unsilence() {
    if (!event) return
    silenceBusy = true
    try {
      const r = await api.unsilence(event.id)
      event = r.event
      deliveries = r.deliveries
      silenceDone = r.deliveries.length ? `Pushed to ${r.deliveries.length} device${r.deliveries.length === 1 ? '' : 's'}.` : 'Unsilenced. No paired phones with push registered, so nothing was sent.'
    } catch (e: any) {
      error = e.message
    } finally {
      silenceBusy = false
    }
  }

  async function silence(field: 'fingerprint' | 'title' | 'source', scoped: boolean) {
    if (!event) return
    silenceBusy = true
    try {
      const value = field === 'fingerprint' ? event.fingerprint : field === 'title' ? event.title : event.source
      const s = await api.createSilence({ field, value, project_id: scoped ? event.project_id : undefined, note: `From event ${event.id}` })
      silenceDone = `Silenced ${field} "${s.value}"${scoped ? ` in ${event.project_name}` : ' in every project'}. Future matches are stored but not pushed.`
      silenceOpen = false
    } catch (e: any) {
      error = e.message
    } finally {
      silenceBusy = false
    }
  }

  $effect(() => {
    event = null
    error = ''
    api
      .event(id)
      .then((e) => {
        event = e
        loadRule(e)
      })
      .catch((e) => (error = e.message))
    api.eventDeliveries(id).then((r) => (deliveries = r.deliveries)).catch(() => {})
  })

  // Recognised sections rendered specially; everything else falls back to the JSON tree.
  const KNOWN = ['exception', 'stacktrace', 'tags', 'context', 'breadcrumbs']
  const data = $derived((event?.data ?? {}) as Record<string, any>)
  const exception = $derived(data.exception && typeof data.exception === 'object' ? data.exception : null)
  const frames = $derived(Array.isArray(data.stacktrace) ? (data.stacktrace as any[]) : null)
  const tags = $derived(data.tags && typeof data.tags === 'object' && !Array.isArray(data.tags) ? (data.tags as Record<string, unknown>) : null)
  const context = $derived(data.context && typeof data.context === 'object' && !Array.isArray(data.context) ? (data.context as Record<string, unknown>) : null)
  const breadcrumbs = $derived(Array.isArray(data.breadcrumbs) ? (data.breadcrumbs as any[]) : null)
  const rest = $derived(Object.fromEntries(Object.entries(data).filter(([k]) => !KNOWN.includes(k))))
  const hasRest = $derived(Object.keys(rest).length > 0)

  function str(v: unknown): string {
    return typeof v === 'string' ? v : v === undefined || v === null ? '' : JSON.stringify(v)
  }
</script>

<div class="stack">
  <div class="crumb-row">
    <div class="crumb"><a href="/" onclick={link}>Inbox</a><span class="faint">/</span><span class="muted mono">{id}</span></div>
    {#if event}
      <Button variant="secondary" size="sm" onclick={() => (silenceOpen = !silenceOpen)} disabled={silenceBusy}>{silenceOpen ? 'Cancel' : 'Silence events like this'}</Button>
    {/if}
  </div>
  {#if event && silenceOpen}
    <div class="silence-menu" in:pop>
      <span class="caption muted">Stop pushes for future events matching:</span>
      {#if event.fingerprint}
        <button type="button" onclick={() => silence('fingerprint', true)}>fingerprint <span class="mono">{event.fingerprint}</span> <span class="muted">· {event.project_name}</span></button>
      {/if}
      <button type="button" onclick={() => silence('title', true)}>title “{event.title}” <span class="muted">· {event.project_name}</span></button>
      <button type="button" onclick={() => silence('title', false)}>title “{event.title}” <span class="muted">· every project</span></button>
      {#if event.source}
        <button type="button" onclick={() => silence('source', true)}>source <span class="mono">{event.source}</span> <span class="muted">· {event.project_name}</span></button>
      {/if}
    </div>
  {/if}

  {#if error}
    <Notice tone="bad">{error}</Notice>
  {:else if !event}
    <Card>
      <Skeleton lines={1} height={12} width="40%" />
      <div style="margin-top: 12px"><Skeleton lines={1} height={24} width="55%" /></div>
      <div style="margin-top: 10px"><Skeleton lines={1} height={14} width="70%" /></div>
      <div class="facts" style="margin-top: 24px">
        <Skeleton lines={2} height={11} widths={['40%', '80%']} />
        <Skeleton lines={2} height={11} widths={['40%', '80%']} />
      </div>
    </Card>
    <Card><Skeleton lines={3} height={12} widths={['30%', '90%', '75%']} /></Card>
    <Card><Skeleton lines={4} height={12} widths={['30%', '95%', '85%', '60%']} /></Card>
  {:else}
    <div class="stack rise">
    {#if event.silenced}
      <Card title="Silenced">
        <p class="secondary lead">This event matched a silence rule and was not pushed to any phone.</p>
        {#if rule}
          <div class="rule">
            <span class="pill">{rule.field}</span>
            <span class="mono">{rule.value}</span>
            <span class="muted caption">· {rule.project_name || 'every project'}{rule.note ? ` · ${rule.note}` : ''}</span>
          </div>
        {:else}
          <p class="muted caption">The rule that silenced it has since been removed.</p>
        {/if}
        <div class="row" style="margin-top: 12px; flex-wrap: wrap">
          <Button variant="secondary" size="sm" onclick={unsilence} disabled={silenceBusy}>Unsilence and push now</Button>
          {#if rule}<Button variant="danger" size="sm" onclick={removeRule} disabled={silenceBusy}>Remove rule</Button>{/if}
          <a href="/?silenced=true" onclick={link} class="small">All silenced events</a>
        </div>
      </Card>
    {/if}
    {#if silenceDone}
      <div transition:panel><Notice tone="good">{silenceDone}</Notice></div>
    {/if}
    <Card>
      <div class="head">
        <div class="meta">
          <span class="secondary proj"><ProjectIcon icon={event.project_icon} size={14} /><span>{event.project_name}</span></span>
          <span class="faint">·</span>
          <LevelBadge level={event.level} />
          {#if event.source}<span class="faint">·</span><span class="muted">{event.source}</span>{/if}
          {#if event.type}<span class="faint">·</span><span class="muted">{event.type}</span>{/if}
        </div>
        <h1>{event.title}</h1>
        {#if event.body}<p class="body">{event.body}</p>{/if}
      </div>

      <div class="facts">
        <div><span class="k">Occurred</span><span title={event.occurred_at}>{fullDate(event.occurred_at)}</span></div>
        <div><span class="k">Received</span><span title={event.created_at}>{fullDate(event.created_at)} · {relative(event.created_at)}</span></div>
        {#if event.fingerprint}<div><span class="k">Fingerprint</span><span class="mono">{event.fingerprint} <a class="small" href={groupPath(event.project_id, event.fingerprint)} onclick={link}>all occurrences</a></span></div>{/if}
        {#if event.external_id}<div><span class="k">External id</span><span class="mono">{event.external_id}</span></div>{/if}
        <div><span class="k">Event id</span><span class="mono">{event.id}</span></div>
      </div>
    </Card>

    {#if event.actions && event.actions.length}
      <Card title="Actions">
        <div class="actions">
          {#each event.actions as a (a.label + a.url)}
            <a class="action" href={a.url} target="_blank" rel="noopener noreferrer">{a.label}<span class="faint">↗</span></a>
          {/each}
        </div>
      </Card>
    {/if}

    {#if exception}
      <Card title="Exception">
        <div class="exc">
          {#if exception.type}<div class="exc-type">{str(exception.type)}</div>{/if}
          {#if exception.message}<div class="exc-msg">{str(exception.message)}</div>{/if}
          {#each Object.entries(exception).filter(([k]) => k !== 'type' && k !== 'message') as [k, v] (k)}
            <div class="kv"><span class="k">{k}</span><span class="mono">{str(v)}</span></div>
          {/each}
        </div>
      </Card>
    {/if}

    {#if frames}
      <Card title="Stacktrace">
        <div class="frames">
          {#each frames as f, i (i)}
            {#if f && typeof f === 'object'}
              <div class="frame" class:inapp={f.in_app === true}>
                <div class="fn">{str(f.function ?? f.module ?? '—')}</div>
                <div class="loc mono">{str(f.file ?? f.filename ?? '')}{#if f.line !== undefined}<span class="faint">:</span>{str(f.line)}{/if}</div>
              </div>
            {:else}
              <div class="frame"><div class="loc mono">{str(f)}</div></div>
            {/if}
          {/each}
        </div>
      </Card>
    {/if}

    {#if tags}
      <Card title="Tags">
        <div class="pills">
          {#each Object.entries(tags) as [k, v] (k)}
            <span class="pill"><span class="pk">{k}</span>{str(v)}</span>
          {/each}
        </div>
      </Card>
    {/if}

    {#if context}
      <Card title="Context">
        <div class="kvs">
          {#each Object.entries(context) as [k, v] (k)}
            {#if v !== null && typeof v === 'object'}
              <div class="kv block"><span class="k">{k}</span><div class="tree"><JsonTree value={v} open /></div></div>
            {:else}
              <div class="kv"><span class="k">{k}</span><span class="mono v">{str(v)}</span></div>
            {/if}
          {/each}
        </div>
      </Card>
    {/if}

    {#if breadcrumbs}
      <Card title="Breadcrumbs">
        <div class="crumbs">
          {#each breadcrumbs as b, i (i)}
            <div class="bc">
              <span class="bc-t muted">{str(b?.timestamp ?? b?.time ?? '')}</span>
              <span class="bc-c secondary">{str(b?.category ?? b?.type ?? '')}</span>
              <span class="bc-m">{str(b?.message ?? b?.msg ?? (typeof b === 'string' ? b : JSON.stringify(b)))}</span>
            </div>
          {/each}
        </div>
      </Card>
    {/if}

    {#if hasRest}
      <Card title="Data">
        <div class="tree">
          {#each Object.entries(rest) as [k, v] (k)}
            <JsonTree value={v} name={k} open />
          {/each}
        </div>
      </Card>
    {/if}

    <Card title="Raw JSON">
      {#snippet action()}
        <button type="button" class="linkish" onclick={() => (showRaw = !showRaw)}>{showRaw ? 'Hide' : 'Show'}</button>
      {/snippet}
      {#if showRaw}
        <div transition:panel><CodeBlock code={JSON.stringify(event, null, 2)} /></div>
      {:else}
        <div class="muted">{Object.keys(data).length} top-level keys</div>
      {/if}
    </Card>

    <Card title="Delivery">
      {#if deliveries.length === 0}
        <div class="muted">No delivery attempts recorded.</div>
      {:else}
        <div class="dls">
          {#each deliveries as d (d.id)}
            <div class="dl">
              <span class="dl-n">{d.target_type === 'webhook' ? `Webhook · ${d.webhook_host}` : d.device_name || d.device_id}</span>
              <StatusDot tone={d.status === 'sent' ? 'ok' : d.status === 'failed' ? 'bad' : 'muted'}>{d.status === 'sent' ? 'Sent' : d.status === 'failed' ? 'Failed' : 'Skipped'}</StatusDot>
              <span class="muted caption">{d.error || d.apns_id || (d.http_status ? `HTTP ${d.http_status}` : '')}</span>
              <span class="muted caption r">{relative(d.attempted_at)}</span>
            </div>
          {/each}
        </div>
      {/if}
    </Card>
    </div>
  {/if}
</div>

<style>
  .crumb { display: flex; gap: 8px; align-items: center; font: var(--up-type-meta); }
  /* Cards rise in one after another once the event has loaded. */
  .rise > :global(*) { animation: rise 220ms cubic-bezier(0.2, 0, 0, 1) both; }
  .rise > :global(:nth-child(1)) { animation-delay: 0ms; }
  .rise > :global(:nth-child(2)) { animation-delay: 40ms; }
  .rise > :global(:nth-child(3)) { animation-delay: 80ms; }
  .rise > :global(:nth-child(4)) { animation-delay: 120ms; }
  .rise > :global(:nth-child(5)) { animation-delay: 160ms; }
  .rise > :global(:nth-child(6)) { animation-delay: 200ms; }
  .rise > :global(:nth-child(n + 7)) { animation-delay: 240ms; }
  @keyframes rise { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: none; } }
  @media (prefers-reduced-motion: reduce) { .rise > :global(*) { animation: none; } }
  .head { display: flex; flex-direction: column; gap: 8px; }
  .proj { display: inline-flex; gap: 6px; align-items: center; }
  .meta { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; font: var(--up-type-meta); }
  h1 { font: var(--up-type-metric); letter-spacing: -0.01em; word-break: break-word; }
  .body { font: var(--up-type-status-line); color: var(--up-text-secondary); white-space: pre-wrap; word-break: break-word; }
  .crumb-row { display: flex; align-items: center; justify-content: space-between; gap: var(--up-space-3); }
  .silence-menu { display: flex; flex-direction: column; gap: 4px; padding: 10px; border: 1px solid var(--up-border-hairline); border-radius: var(--up-radius-tooltip); background: var(--up-bg); }
  .rule { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; font: var(--up-type-meta); }
  .pill { font: var(--up-type-code); padding: 3px 10px; border-radius: var(--up-radius-pill); background: var(--up-accent-tint); color: var(--up-accent-hover); }
  .lead { font: var(--up-type-meta); line-height: 1.6; }
  .silence-menu button { text-align: left; background: none; border: none; cursor: pointer; font: var(--up-type-meta); color: var(--up-ink); padding: 8px 10px; border-radius: var(--up-radius-control); transition: background 120ms ease-out; }
  .silence-menu button:hover { background: var(--up-bg-hover); }
  .facts { display: grid; grid-template-columns: 1fr 1fr; gap: 8px 24px; margin-top: var(--up-space-5); padding-top: var(--up-space-4); border-top: 1px solid var(--up-border-hairline); }
  .facts > div { display: flex; flex-direction: column; gap: 2px; font: var(--up-type-meta); min-width: 0; }
  .facts span:last-child { overflow: hidden; text-overflow: ellipsis; }
  .k { font: var(--up-type-caption); color: var(--up-text-muted); }
  .exc { display: flex; flex-direction: column; gap: 6px; }
  .exc-type { font: var(--up-type-row-title); }
  .exc-msg { font: var(--up-type-code); color: var(--up-text-secondary); white-space: pre-wrap; word-break: break-word; }
  .frames { display: flex; flex-direction: column; gap: 3px; }
  .frame { padding: 8px 12px; border-radius: var(--up-radius-row); background: var(--up-bg-hover); display: flex; flex-direction: column; gap: 2px; }
  .frame.inapp { background: var(--up-accent-tint); box-shadow: var(--up-ring-inset); }
  .fn { font: var(--up-type-ui); color: var(--up-ink); word-break: break-all; }
  .loc { color: var(--up-text-secondary); word-break: break-all; }
  .pills { display: flex; flex-wrap: wrap; gap: 8px; }
  .pill { display: inline-flex; gap: 6px; font: var(--up-type-small); padding: 4px 10px; border-radius: var(--up-radius-pill); background: var(--up-bg-hover); box-shadow: var(--up-ring-inset); }
  .pk { color: var(--up-text-muted); font-weight: 500; }
  .kvs { display: flex; flex-direction: column; gap: 10px; }
  .kv { display: grid; grid-template-columns: 140px 1fr; gap: 12px; align-items: baseline; font: var(--up-type-meta); }
  .kv.block { align-items: start; }
  .kv .v { word-break: break-all; }
  .tree { overflow-x: auto; }
  .crumbs { display: flex; flex-direction: column; gap: 6px; }
  .bc { display: grid; grid-template-columns: 110px 110px 1fr; gap: 12px; font: var(--up-type-meta); }
  .bc-m { word-break: break-word; }
  .linkish { background: none; border: none; cursor: pointer; font: var(--up-type-ui); color: var(--up-accent); padding: 0; }
  .linkish:hover { color: var(--up-accent-hover); }
  .actions { display: flex; flex-wrap: wrap; gap: 8px; }
  .action { display: inline-flex; align-items: center; gap: 6px; font: var(--up-type-ui); height: 34px; padding: 0 16px; border-radius: var(--up-radius-control); background: var(--up-accent); color: var(--up-text-on-dark); }
  .action:hover { background: var(--up-accent-hover); color: var(--up-text-on-dark); }
  .action .faint { color: inherit; opacity: 0.7; }
  .dls { display: flex; flex-direction: column; gap: 8px; }
  .dl { display: grid; grid-template-columns: 1fr auto 1fr auto; gap: 12px; align-items: center; font: var(--up-type-meta); }
  .r { text-align: right; }
  @media (max-width: 600px) {
    .facts, .kv, .bc { grid-template-columns: 1fr; }
  }
</style>
