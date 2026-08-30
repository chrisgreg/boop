<script lang="ts">
  import { api } from '../api'
  import { currentWebPushSubscription, subscribeBrowser, webPushEnvironment } from '../web-push'
  import Button from './Button.svelte'
  import Notice from './Notice.svelte'
  import StatusDot from './StatusDot.svelte'

  let { onchange }: { onchange?: () => void } = $props()

  type PushState = 'checking' | 'unsupported' | 'needs_install' | 'available' | 'enabled' | 'denied'
  let pushState = $state<PushState>('checking')
  let publicKey = $state('')
  let busy = $state(false)
  let error = $state('')
  const environment = webPushEnvironment()

  async function inspect() {
    error = ''
    if (!environment.supported) {
      pushState = 'unsupported'
      return
    }
    if (environment.ios && !environment.installed) {
      pushState = 'needs_install'
      return
    }
    try {
      const [config, subscription] = await Promise.all([api.webPushConfig(), currentWebPushSubscription()])
      publicKey = config.public_key
      if (!config.enabled || !config.public_key) {
        pushState = 'unsupported'
      } else if (subscription) {
        pushState = 'enabled'
      } else if (Notification.permission === 'denied') {
        pushState = 'denied'
      } else {
        pushState = 'available'
      }
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Could not inspect Web Push.'
      pushState = 'available'
    }
  }

  $effect(() => {
    void inspect()
  })

  async function enable() {
    busy = true
    error = ''
    try {
      const subscription = await subscribeBrowser(publicKey)
      const json = subscription.toJSON()
      if (!json.endpoint || !json.keys?.p256dh || !json.keys.auth) throw new Error('The browser returned an incomplete push subscription.')
      await api.subscribeWebPush({
        endpoint: json.endpoint,
        keys: { p256dh: json.keys.p256dh, auth: json.keys.auth },
        name: environment.ios ? 'iPhone Home Screen' : environment.installed ? 'Installed web app' : 'Web browser',
      })
      pushState = 'enabled'
      onchange?.()
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Could not enable Web Push.'
      if (Notification.permission === 'denied') pushState = 'denied'
    } finally {
      busy = false
    }
  }

  async function disable() {
    busy = true
    error = ''
    try {
      const subscription = await currentWebPushSubscription()
      if (subscription) {
        await api.unsubscribeWebPush(subscription.endpoint)
        await subscription.unsubscribe()
      }
      pushState = 'available'
      onchange?.()
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Could not disable Web Push.'
    } finally {
      busy = false
    }
  }
</script>

<div class="control">
  {#if pushState === 'checking'}
    <span class="muted">Checking this browser…</span>
  {:else if pushState === 'enabled'}
    <div class="row spread">
      <StatusDot tone="ok">Notifications enabled on this device</StatusDot>
      <Button variant="secondary" size="sm" onclick={disable} disabled={busy}>{busy ? 'Disabling' : 'Disable'}</Button>
    </div>
  {:else if pushState === 'needs_install'}
    <Notice tone="info">On iPhone, open this page in Safari, choose Share → Add to Home Screen, then open Boop from the new Home Screen icon.</Notice>
  {:else if pushState === 'denied'}
    <Notice tone="warn">Notifications are blocked. Allow them for Boop in iPhone Settings, then return here.</Notice>
  {:else if pushState === 'unsupported'}
    <Notice tone="warn">This browser does not support standards-based Web Push. On iPhone, use the Boop Home Screen app on iOS 16.4 or newer.</Notice>
  {:else}
    <div class="row spread">
      <span class="secondary">Receive Boop alerts as system notifications on this device.</span>
      <Button onclick={enable} disabled={busy || !publicKey}>{busy ? 'Enabling' : 'Enable notifications'}</Button>
    </div>
  {/if}
  {#if error}<Notice tone="bad">{error}</Notice>{/if}
</div>

<style>
  .control { display: flex; flex-direction: column; gap: 12px; }
  .spread { justify-content: space-between; flex-wrap: wrap; }
</style>
