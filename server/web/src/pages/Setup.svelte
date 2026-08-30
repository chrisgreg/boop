<script lang="ts">
  import { api, type Status, type ProjectCreated } from '../lib/api'
  import { router } from '../lib/router.svelte'
  import Card from '../lib/ui/Card.svelte'
  import Button from '../lib/ui/Button.svelte'
  import Input from '../lib/ui/Input.svelte'
  import CodeBlock from '../lib/ui/CodeBlock.svelte'
  import StatusDot from '../lib/ui/StatusDot.svelte'
  import Notice from '../lib/ui/Notice.svelte'
  import Devices from './Devices.svelte'
  import { fly } from 'svelte/transition'
  import { cubicOut } from 'svelte/easing'
  import { dur, panel } from '../lib/motion'

  let { onfinished }: { onfinished: () => void } = $props()

  const STEPS = ['Server', 'Apple Push Notifications', 'iPhone', 'First project', 'Test Boop']
  let step = $state(0)
  let status = $state<Status | null>(null)
  let error = $state('')
  let projectName = $state('')
  let created = $state<ProjectCreated | null>(null)
  let testing = $state(false)
  let testMsg = $state('')
  let paired = $state(false)

  async function refresh() {
    try {
      status = await api.status()
      paired = (status?.pushable_devices ?? 0) > 0 || (status?.devices ?? 0) > 0
    } catch (e: any) {
      error = e.message
    }
  }
  $effect(() => {
    refresh()
  })

  async function createProject() {
    if (!projectName.trim()) return
    try {
      created = await api.createProject({ name: projectName.trim() })
      await refresh()
    } catch (e: any) {
      error = e.message
    }
  }

  async function sendTest() {
    testing = true
    testMsg = ''
    try {
      const r = await api.test()
      if (r.deliveries.length === 0) testMsg = 'Event created. No phone with push registered yet, so nothing was sent.'
      else if (!r.apns_configured) testMsg = 'Event created, but APNs is not configured so the push was skipped.'
      else if (r.deliveries.every((d) => d.status === 'sent')) testMsg = `Sent to ${r.deliveries.length} device${r.deliveries.length === 1 ? '' : 's'}. Check your phone.`
      else testMsg = 'Push failed: ' + r.deliveries.map((d) => d.error).join('; ')
    } catch (e: any) {
      testMsg = e.message
    } finally {
      testing = false
    }
  }

  async function finish() {
    try {
      await api.updateSettings({ setup_completed: true })
      onfinished()
      router.navigate('/', true)
    } catch (e: any) {
      error = e.message
    }
  }

  const origin = typeof location !== 'undefined' ? location.origin : ''
</script>

<div class="setup">
  <div class="hero">
    <h1>Welcome to Boop</h1>
    <p class="secondary">A few steps to your first push notification.</p>
  </div>

  <ol class="steps">
    {#each STEPS as s, i (s)}
      <li class:done={i < step} class:active={i === step}><button type="button" onclick={() => (step = i)}><span class="num">{i + 1}</span>{s}</button></li>
    {/each}
  </ol>

  {#if error}<div transition:panel><Notice tone="bad">{error}</Notice></div>{/if}

  {#key step}
  <div in:fly={{ x: 12, duration: dur(180), easing: cubicOut }} class="step-body">
  {#if step === 0}
    <Card title="Server">
      <div class="facts">
        <div><span class="k">Server</span><StatusDot tone="ok">Running</StatusDot></div>
        <div><span class="k">Database</span>{#if status?.database === 'ok'}<StatusDot tone="ok">Healthy</StatusDot>{:else}<StatusDot tone="bad">{status?.database ?? 'checking'}</StatusDot>{/if}</div>
        <div><span class="k">Database path</span><span class="mono">{status?.database_path ?? '—'}</span></div>
        <div><span class="k">Base URL</span><span class="mono">{status?.base_url ?? '—'}</span></div>
        <div><span class="k">Version</span><span>{status?.version ?? '—'}</span></div>
      </div>
      <p class="lead secondary" style="margin-top: 16px">Your phone must be able to reach the base URL over HTTPS. If it shows a local address, set BOOP_BASE_URL to your public address and put Boop behind your reverse proxy.</p>
      <p class="lead secondary">Back up by copying the database file. The APNs key should be backed up separately.</p>
    </Card>
  {:else if step === 1}
    <Card title="Apple Push Notifications">
      {#if status?.apns.configured}
        <StatusDot tone="ok">Configured · {status.apns.bundle_id} · {status.apns.environment}</StatusDot>
      {:else}
        <StatusDot tone="warn">Not configured{status?.apns.error ? ` · ${status.apns.error}` : ''}</StatusDot>
        <p class="lead secondary" style="margin-top: 12px">Boop works without this, but pushes are skipped until it is set. You can come back later.</p>
      {/if}
      <ol class="howto">
        <li>Sign in to the Apple Developer portal and create an App identifier for the Boop app.</li>
        <li>Enable the Push Notifications capability on it.</li>
        <li>Under Keys, create an Apple Push Notifications service (APNs) key and download the .p8 file. It can only be downloaded once.</li>
        <li>Note the Team id (top right of the portal) and the Key id shown next to the key.</li>
        <li>Use the same bundle identifier in Xcode when you build the iOS app.</li>
        <li>Mount the .p8 into the container and set the environment below, then restart Boop.</li>
      </ol>
      <CodeBlock code={`APNS_TEAM_ID=YOUR_TEAM_ID\nAPNS_KEY_ID=YOUR_KEY_ID\nAPNS_BUNDLE_ID=com.example.Boop\nAPNS_PRIVATE_KEY_PATH=/run/secrets/apns.p8`} />
      <div class="actions"><Button variant="secondary" size="sm" onclick={refresh}>Re-check</Button></div>
    </Card>
  {:else if step === 2}
    <Card title="iPhone">
      <p class="lead secondary">Build the open-source Boop iOS app with your own Apple team and bundle identifier, install it on your phone, then pair it here.</p>
      {#if paired}
        <div style="margin-top: 12px"><StatusDot tone="ok">{status?.devices} device{status?.devices === 1 ? '' : 's'} paired</StatusDot></div>
      {/if}
    </Card>
    <Devices embedded onpaired={refresh} />
  {:else if step === 3}
    <Card title="First project">
      {#if created}
        <p class="lead secondary">Copy this API key now. It is shown once.</p>
        <CodeBlock code={created.api_key} />
        <p class="lead muted" style="margin-top: 16px">Send an event:</p>
        <CodeBlock code={`curl ${origin}/api/v1/events \\\n  -H "Authorization: Bearer ${created.api_key}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"title": "Deploy complete", "level": "success"}'`} />
      {:else if (status?.projects ?? 0) > 0}
        <p class="lead secondary">You already have {status?.projects} project{status?.projects === 1 ? '' : 's'}. Manage them from the Projects tab after setup.</p>
      {:else}
        <p class="lead secondary">Projects group events and each has its own API key. Name it after the app or system that will send events.</p>
        <form
          class="row"
          style="margin-top: 12px"
          onsubmit={(e) => {
            e.preventDefault()
            createProject()
          }}
        >
          <Input bind:value={projectName} placeholder="e.g. Uini" aria-label="Project name" style="width: 240px" />
          <Button type="submit" disabled={!projectName.trim()}>Create project</Button>
        </form>
      {/if}
    </Card>
  {:else}
    <Card title="Test Boop">
      <p class="lead secondary">Create a test event and push it to every paired phone.</p>
      <div class="row" style="margin-top: 12px">
        <Button onclick={sendTest} disabled={testing || (status?.projects ?? 0) === 0}>{testing ? 'Sending' : 'Send test notification'}</Button>
        {#if (status?.projects ?? 0) === 0}<span class="muted">Create a project first.</span>{/if}
      </div>
      {#if testMsg}<div style="margin-top: 16px" transition:panel><Notice tone="info">{testMsg}</Notice></div>{/if}
    </Card>
  {/if}
  </div>
  {/key}

  <div class="nav">
    <Button variant="secondary" onclick={() => (step = Math.max(0, step - 1))} disabled={step === 0}>Back</Button>
    <span class="spacer"></span>
    <Button variant="ghost" onclick={finish}>Skip setup</Button>
    {#if step < STEPS.length - 1}
      <Button onclick={() => (step = step + 1)}>Next</Button>
    {:else}
      <Button onclick={finish}>Finish</Button>
    {/if}
  </div>
</div>

<style>
  .setup { display: flex; flex-direction: column; gap: var(--up-space-5); }
  .step-body { display: flex; flex-direction: column; gap: var(--up-space-5); }
  .hero { padding: var(--up-space-6) 0 0; display: flex; flex-direction: column; gap: 6px; }
  h1 { font: var(--up-type-metric); letter-spacing: -0.01em; }
  .hero p { font: var(--up-type-status-line); }
  .steps { list-style: none; margin: 0; padding: 0; display: flex; gap: var(--up-space-5); border-bottom: 1px solid var(--up-border-hairline); overflow-x: auto; }
  .steps button { background: none; border: none; cursor: pointer; padding: 0 0 10px; font: var(--up-type-ui); color: var(--up-text-muted); display: inline-flex; gap: 8px; align-items: center; white-space: nowrap; }
  .steps .active button { color: var(--up-ink); box-shadow: inset 0 -2px 0 var(--up-accent); }
  .steps .done button { color: var(--up-text-secondary); }
  .num { width: 18px; height: 18px; border-radius: 50%; background: var(--up-bg-hover); box-shadow: var(--up-ring-inset); font: var(--up-type-caption); display: inline-flex; align-items: center; justify-content: center; }
  .active .num { background: var(--up-accent); color: var(--up-text-on-dark); box-shadow: none; }
  .done .num { background: var(--up-operational); }
  .facts { display: grid; grid-template-columns: 1fr 1fr; gap: 12px 24px; }
  .facts > div { display: flex; flex-direction: column; gap: 3px; font: var(--up-type-meta); min-width: 0; }
  .facts span:last-child { overflow: hidden; text-overflow: ellipsis; }
  .k { font: var(--up-type-caption); color: var(--up-text-muted); }
  .lead { font: var(--up-type-meta); line-height: 1.6; margin-top: 8px; }
  .howto { font: var(--up-type-meta); color: var(--up-text-secondary); line-height: 1.7; padding-left: 20px; margin: 12px 0 16px; }
  .actions { display: flex; justify-content: flex-end; margin-top: 12px; }
  .nav { display: flex; gap: var(--up-space-3); align-items: center; }
  .spacer { flex: 1; }
</style>
