<script lang="ts">
  import { api, setLoginHandler } from './lib/api'
  import { router, link } from './lib/router.svelte'
  import Tabs from './lib/ui/Tabs.svelte'
  import StatusDot from './lib/ui/StatusDot.svelte'
  import Inbox from './pages/Inbox.svelte'
  import EventDetail from './pages/EventDetail.svelte'
  import Group from './pages/Group.svelte'
  import Projects from './pages/Projects.svelte'
  import Devices from './pages/Devices.svelte'
  import Settings from './pages/Settings.svelte'
  import Setup from './pages/Setup.svelte'
  import Login from './pages/Login.svelte'
  import { pageIn } from './lib/motion'

  let setupCompleted = $state<boolean | null>(null)
  let pushReady = $state<boolean | null>(null)
  let unreachable = $state(false)
  let authRequired = $state(false)
  let needsLogin = $state(false)
  setLoginHandler(() => (needsLogin = true))

  async function boot() {
    try {
      const me = await api.me()
      authRequired = me.auth_required
      if (me.auth_required && !me.authenticated) {
        needsLogin = true
        unreachable = false
        return
      }
      needsLogin = false
      const s = await api.status()
      setupCompleted = s.setup_completed
      pushReady = (s.apns.configured && s.pushable_devices > 0) || s.web_push.subscriptions > 0
      unreachable = false
      if (!s.setup_completed && router.route.name !== 'setup') router.navigate('/setup', true)
    } catch {
      unreachable = true
    }
  }
  $effect(() => {
    boot()
    const t = setInterval(boot, 60_000)
    return () => clearInterval(t)
  })

  async function signOut() {
    await api.logout().catch(() => {})
    needsLogin = true
  }

  const route = $derived(router.route)
  const tabs = [
    { label: 'Inbox', href: '/' },
    { label: 'Projects', href: '/projects' },
    { label: 'Devices', href: '/devices' },
    { label: 'Settings', href: '/settings' },
  ]
  const activeTab = $derived(route.name === 'event' || route.name === 'group' ? '/' : router.path)
</script>

<div class="page">
  <header>
    <a class="brand" href="/" onclick={link}>
      <span class="mark"></span>
      <span class="wordmark">Boop</span>
    </a>
    <div class="right">
      {#if unreachable}
        <StatusDot tone="bad">Server unreachable</StatusDot>
      {:else if needsLogin}
        <span></span>
      {:else if pushReady === false}
        <a href="/settings" onclick={link} class="plain"><StatusDot tone="warn">Push not enabled</StatusDot></a>
      {:else if pushReady}
        <StatusDot tone="ok">Push ready</StatusDot>
      {/if}
      {#if authRequired && !needsLogin}
        <button type="button" class="signout" onclick={signOut}>Sign out</button>
      {/if}
    </div>
  </header>

  {#if needsLogin}
    <Login onsuccess={boot} />
  {:else if route.name === 'setup'}
    <Setup onfinished={() => (setupCompleted = true)} />
  {:else}
    <div class="tabs"><Tabs items={tabs} active={activeTab} /></div>
    {#key router.path}
      <div in:pageIn>
        {#if route.name === 'inbox'}
          <Inbox />
        {:else if route.name === 'event'}
          <EventDetail id={route.params.id} />
        {:else if route.name === 'group'}
          <Group project={route.params.project} fingerprint={route.params.fingerprint} />
        {:else if route.name === 'projects'}
          <Projects />
        {:else if route.name === 'devices'}
          <Devices />
        {:else if route.name === 'settings'}
          <Settings />
        {:else}
          <p class="muted">Nothing here. <a href="/" onclick={link}>Back to the inbox</a>.</p>
        {/if}
      </div>
    {/key}
  {/if}

  <footer class="caption faint">Boop · self-hosted · <a href="/settings" onclick={link}>status</a></footer>
</div>

<style>
  header { display: flex; align-items: center; justify-content: space-between; padding: 22px 0; }
  .brand { display: inline-flex; align-items: center; gap: 10px; color: var(--up-ink); }
  .brand:hover { color: var(--up-ink); }
  .mark { width: 22px; height: 22px; border-radius: var(--up-radius-pill); background: var(--up-accent); position: relative; }
  .mark::after { content: ''; position: absolute; inset: 7px; border-radius: 50%; background: var(--up-bg); }
  .wordmark { font: var(--up-type-wordmark); letter-spacing: 0.02em; }
  .right { display: flex; align-items: center; gap: 18px; }
  .plain { color: inherit; }
  .signout { background: none; border: none; cursor: pointer; padding: 0; font: var(--up-type-ui); color: var(--up-text-muted); }
  .signout:hover { color: var(--up-ink); }
  .tabs { margin-bottom: var(--up-space-5); }
  footer { margin-top: var(--up-space-6); padding-top: var(--up-space-4); border-top: 1px solid var(--up-border-hairline); }
</style>
