<script lang="ts">
  import { api, LEVELS, type Level, type Project, type ProjectCreated, type Webhook, type WebhookInput, type WebhookPayloadMode } from '../lib/api'
  import { LEVEL_LABEL } from '../lib/levels'
  import { relative } from '../lib/format'
  import Card from '../lib/ui/Card.svelte'
  import Button from '../lib/ui/Button.svelte'
  import Input from '../lib/ui/Input.svelte'
  import Select from '../lib/ui/Select.svelte'
  import Switch from '../lib/ui/Switch.svelte'
  import SettingRow from '../lib/ui/SettingRow.svelte'
  import CodeBlock from '../lib/ui/CodeBlock.svelte'
  import Notice from '../lib/ui/Notice.svelte'
  import Empty from '../lib/ui/Empty.svelte'
  import ProjectIcon from '../lib/ui/ProjectIcon.svelte'
  import IconPicker from '../lib/ui/IconPicker.svelte'
  import { panel, soft, pop, reorder } from '../lib/motion'
  import Skeleton from '../lib/ui/Skeleton.svelte'
  import { webhookPresets } from '../lib/webhookPresets'

  let projects = $state<Project[]>([])
  let loaded = $state(false)
  let error = $state('')
  let newName = $state('')
  let creating = $state(false)
  let revealed = $state<ProjectCreated | null>(null)
  let editing = $state<string | null>(null)
  let confirmDelete = $state<string | null>(null)
  let webhooks = $state<Webhook[]>([])
  let editingWebhook = $state<string | null>(null)
  let webhookURL = $state('')
  let webhookMode = $state<WebhookPayloadMode>('json')
  let webhookTemplate = $state('')
  let webhookHeaders = $state('{}')
  let webhookMinLevel = $state<Level | ''>('')
  let webhookEnabled = $state(true)
  let webhookResult = $state('')

  async function load() {
    try {
      projects = (await api.projects()).projects
    } catch (e: any) {
      error = e.message
    } finally {
      loaded = true
    }
  }
  $effect(() => {
    load()
  })

  async function create() {
    if (!newName.trim()) return
    creating = true
    error = ''
    try {
      revealed = await api.createProject({ name: newName.trim() })
      newName = ''
      await load()
    } catch (e: any) {
      error = e.message
    } finally {
      creating = false
    }
  }

  async function patch(p: Project, patch: Partial<Project>) {
    try {
      const updated = await api.updateProject(p.id, patch)
      projects = projects.map((x) => (x.id === p.id ? updated : x))
    } catch (e: any) {
      error = e.message
    }
  }

  async function rotate(p: Project) {
    try {
      revealed = await api.rotateKey(p.id)
    } catch (e: any) {
      error = e.message
    }
  }

  async function remove(p: Project) {
    try {
      await api.deleteProject(p.id)
      confirmDelete = null
      await load()
    } catch (e: any) {
      error = e.message
    }
  }

  async function toggleSettings(p: Project) {
    if (editing === p.id) {
      editing = null
      return
    }
    editing = p.id
    await loadWebhooks(p.id)
  }

  async function loadWebhooks(projectID: string) {
    try {
      webhooks = (await api.webhooks(projectID)).webhooks
    } catch (e: any) {
      error = e.message
    }
  }

  function resetWebhookForm() {
    editingWebhook = null
    webhookURL = ''
    webhookMode = 'json'
    webhookTemplate = ''
    webhookHeaders = '{}'
    webhookMinLevel = ''
    webhookEnabled = true
    webhookResult = ''
  }

  function editWebhook(w: Webhook) {
    editingWebhook = w.id
    webhookURL = w.url
    webhookMode = w.payload_mode
    webhookTemplate = w.body_template
    webhookHeaders = JSON.stringify(w.headers, null, 2)
    webhookMinLevel = w.min_level
    webhookEnabled = w.enabled
    webhookResult = ''
  }

  function applyPreset(name: keyof typeof webhookPresets) {
    const preset = webhookPresets[name]
    webhookMode = preset.payload_mode
    webhookTemplate = preset.body_template
    webhookHeaders = preset.headers
  }

  function webhookInput(): WebhookInput | null {
    const input: WebhookInput = {
      url: webhookURL.trim(), payload_mode: webhookMode, body_template: webhookTemplate,
      min_level: webhookMinLevel, enabled: webhookEnabled,
    }
    if (!webhookHeaders.includes('********')) {
      try {
        input.headers = JSON.parse(webhookHeaders)
      } catch {
        error = 'Webhook headers must be a JSON object.'
        return null
      }
    }
    return input
  }

  async function saveWebhook(p: Project) {
    const input = webhookInput()
    if (!input) return
    try {
      if (editingWebhook) await api.updateWebhook(p.id, editingWebhook, input)
      else await api.createWebhook(p.id, input)
      await loadWebhooks(p.id)
      resetWebhookForm()
    } catch (e: any) {
      error = e.message
    }
  }

  async function removeWebhook(p: Project, w: Webhook) {
    try {
      await api.deleteWebhook(p.id, w.id)
      await loadWebhooks(p.id)
      if (editingWebhook === w.id) resetWebhookForm()
    } catch (e: any) {
      error = e.message
    }
  }

  async function testWebhook(p: Project, w: Webhook) {
    try {
      const { delivery } = await api.testWebhook(p.id, w.id)
      webhookResult = delivery.status === 'sent' ? `Test delivered (HTTP ${delivery.http_status ?? 'success'}).` : `Test ${delivery.status}: ${delivery.error ?? 'unknown error'}`
    } catch (e: any) {
      error = e.message
    }
  }

  const levelOptions = LEVELS.map((l) => ({ value: l, label: LEVEL_LABEL[l] }))
  const webhookLevelOptions = [{ value: '', label: 'Project default' }, ...levelOptions]
  const origin = typeof location !== 'undefined' ? location.origin : 'https://boop.example.com'
</script>

<div class="stack">
  {#if error}<div transition:panel><Notice tone="bad">{error}</Notice></div>{/if}

  {#if revealed}
    <div transition:panel>
    <Card title="API key for {revealed.name}">
      <p class="secondary lead">Copy this key now. It is shown once and only a hash is stored.</p>
      <CodeBlock code={revealed.api_key} />
      <p class="muted lead" style="margin-top: 16px">Send your first boop:</p>
      <CodeBlock
        code={`curl ${origin}/api/v1/events \\\n  -H "Authorization: Bearer ${revealed.api_key}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"title": "Hello from ${revealed.name}", "level": "success"}'`}
      />
      <div class="actions"><Button variant="secondary" onclick={() => (revealed = null)}>Done</Button></div>
    </Card>
    </div>
  {/if}

  <Card title="New project">
    <form
      class="new"
      onsubmit={(e) => {
        e.preventDefault()
        create()
      }}
    >
      <Input bind:value={newName} placeholder="Project name" aria-label="Project name" maxlength={80} required />
      <Button type="submit" disabled={creating || !newName.trim()}>Create</Button>
    </form>
    <p class="muted caption" style="margin-top: 8px">Each project gets its own API key and a shape icon you can change in its settings.</p>
  </Card>

  {#if !loaded}
    {#each [0, 1] as i (i)}
      <Card>
        <Skeleton lines={2} height={13} widths={['35%', '55%']} />
      </Card>
    {/each}
  {:else if projects.length === 0}
    <Card><Empty title="No projects yet">Create one above to get an API key.</Empty></Card>
  {/if}

  {#each projects as p (p.id)}
    <div animate:reorder in:soft>
    <Card compact>
      <div class="phead">
        <ProjectIcon icon={p.icon} size={20} />
        <div class="ptext">
          <div class="pname"><span class="n">{p.name}</span><span class="mono muted caption">{p.slug}</span></div>
          <div class="pmeta muted caption">{p.notify ? `notify ≥ ${LEVEL_LABEL[p.min_level].toLowerCase()}` : 'notifications off'} · created {relative(p.created_at)}</div>
        </div>
        <Button variant="ghost" size="sm" onclick={() => toggleSettings(p)}>{editing === p.id ? 'Close' : 'Settings'}</Button>
      </div>

      {#if editing === p.id}
        <div class="edit" transition:panel>
          <SettingRow label="Name">
            <Input value={p.name} onchange={(e) => patch(p, { name: (e.currentTarget as HTMLInputElement).value })} style="width: 200px" />
          </SettingRow>
          <SettingRow label="Icon" hint="An abstract shape from the palette, shown next to the project name in the inbox and on your phone.">
            <IconPicker value={p.icon} onchange={(v) => patch(p, { icon: v })} />
          </SettingRow>
          <SettingRow label="Push notifications" hint="Turn off to store events without notifying your phone.">
            <Switch checked={p.notify} onchange={(v) => patch(p, { notify: v })} label="Push notifications" />
          </SettingRow>
          <SettingRow label="Minimum level" hint="Only events at or above this level trigger a push.">
            <Select value={p.min_level} options={levelOptions} onchange={(e) => patch(p, { min_level: (e.currentTarget as HTMLSelectElement).value as Project['min_level'] })} style="width: 150px" />
          </SettingRow>
          <SettingRow label="API key" hint="Rotating immediately invalidates the current key.">
            <Button variant="secondary" size="sm" onclick={() => rotate(p)}>Rotate key</Button>
          </SettingRow>
          <section class="webhook-section">
            <h3>Webhooks</h3>
            <p class="muted caption">Webhooks fire independently of phone push notifications. Use a silence rule to stop every channel.</p>
            {#each webhooks as w (w.id)}
              <div class="webhook-row">
                <div class="webhook-detail">
                  <strong>{w.url}</strong>
                  <span class="muted caption">{w.payload_mode}{w.min_level ? ` · ≥ ${w.min_level}` : ' · project minimum'}</span>
                </div>
                <Switch checked={w.enabled} label="Webhook enabled" onchange={async (enabled) => { await api.updateWebhook(p.id, w.id, { enabled }); await loadWebhooks(p.id) }} />
                <Button variant="ghost" size="sm" onclick={() => editWebhook(w)}>Edit</Button>
                <Button variant="ghost" size="sm" onclick={() => testWebhook(p, w)}>Send test</Button>
                <Button variant="danger" size="sm" onclick={() => removeWebhook(p, w)}>Delete</Button>
              </div>
            {/each}
            {#if webhookResult}<p class="caption muted">{webhookResult}</p>{/if}
            <div class="webhook-form">
              <div class="form-head"><strong>{editingWebhook ? 'Edit webhook' : 'Add webhook'}</strong><span class="presets">Presets: <button type="button" onclick={() => applyPreset('Slack')}>Slack</button> · <button type="button" onclick={() => applyPreset('Discord')}>Discord</button></span></div>
              <Input bind:value={webhookURL} placeholder="https://hooks.example.com/..." aria-label="Webhook URL" mono />
              <Select bind:value={webhookMode} options={[{ value: 'json', label: 'Native JSON' }, { value: 'custom', label: 'Custom template' }]} aria-label="Webhook payload mode" />
              {#if webhookMode === 'custom'}
                <label>Body template <textarea bind:value={webhookTemplate} placeholder={'{"text": {{json .Title}}}'}></textarea></label>
                <p class="muted caption">Fields: <code>.Title</code>, <code>.Body</code>, <code>.Level</code>, <code>.Source</code>, <code>.Project.Name</code>. Wrap any interpolated value in <code>{'{{json .Field}}'}</code> — it emits its own quotes, so don't put quotes around it.</p>
              {/if}
              <label>Headers <textarea bind:value={webhookHeaders} aria-label="Webhook headers JSON"></textarea></label>
              <div class="form-controls">
                <Select bind:value={webhookMinLevel} options={webhookLevelOptions} aria-label="Webhook minimum level" />
                <span class="row"><Switch bind:checked={webhookEnabled} label="Webhook enabled" /> Enabled</span>
                <Button size="sm" onclick={() => saveWebhook(p)}>{editingWebhook ? 'Save webhook' : 'Add webhook'}</Button>
                {#if editingWebhook}<Button variant="secondary" size="sm" onclick={resetWebhookForm}>Cancel</Button>{/if}
              </div>
            </div>
          </section>
          <SettingRow label="Delete project" hint="Removes the project and every event it received.">
            {#if confirmDelete === p.id}
              <div class="row" in:pop>
                <Button variant="secondary" size="sm" onclick={() => (confirmDelete = null)}>Cancel</Button>
                <Button variant="danger" size="sm" onclick={() => remove(p)}>Confirm delete</Button>
              </div>
            {:else}
              <Button variant="danger" size="sm" onclick={() => (confirmDelete = p.id)}>Delete</Button>
            {/if}
          </SettingRow>
        </div>
      {/if}
    </Card>
    </div>
  {/each}
</div>

<style>
  .lead { font: var(--up-type-meta); line-height: 1.6; margin-bottom: 12px; }
  .actions { display: flex; justify-content: flex-end; margin-top: var(--up-space-4); }
  .new { display: flex; gap: var(--up-space-3); align-items: center; }
  .new :global(input:first-child) { flex: 1; }
  .phead { display: flex; align-items: center; gap: 12px; }
  .ptext { flex: 1; min-width: 0; }
  .pname { display: flex; align-items: baseline; gap: 8px; min-width: 0; }
  .n { font: var(--up-type-row-title); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .pmeta { margin-top: 1px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .edit { margin-top: var(--up-space-4); }
  .webhook-section { margin: var(--up-space-4) 0; padding: var(--up-space-4) 0; border-top: 1px solid var(--up-border-control); }
  .webhook-section h3 { margin: 0 0 4px; font: var(--up-type-row-title); }
  .webhook-row { display: flex; align-items: center; gap: var(--up-space-2); padding: var(--up-space-3) 0; border-bottom: 1px solid var(--up-border-control); }
  .webhook-detail { flex: 1; min-width: 0; display: grid; gap: 2px; }
  .webhook-detail strong { font: var(--up-type-code); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .webhook-form { display: grid; gap: var(--up-space-3); margin-top: var(--up-space-4); padding: var(--up-space-3); background: var(--up-bg-hover); border-radius: var(--up-radius-control); }
  .form-head, .form-controls { display: flex; align-items: center; gap: var(--up-space-3); flex-wrap: wrap; }
  .presets { margin-left: auto; }
  .presets button { color: var(--up-accent); background: none; border: 0; padding: 0; cursor: pointer; font: inherit; }
  label { display: grid; gap: 4px; font: var(--up-type-meta); }
  textarea { min-height: 72px; width: 100%; resize: vertical; box-sizing: border-box; padding: 8px 10px; border-radius: var(--up-radius-control); border: 1px solid var(--up-border-control); background: var(--up-bg); color: var(--up-ink); font: var(--up-type-code); }
  @media (max-width: 680px) { .webhook-row { align-items: flex-start; flex-wrap: wrap; } .webhook-detail { width: 100%; flex-basis: 100%; } }
  @media (max-width: 520px) {
    .new { flex-wrap: wrap; }
  }
</style>
