export interface WebPushEnvironment {
  supported: boolean
  installed: boolean
  ios: boolean
}

type NavigatorWithStandalone = Navigator & { standalone?: boolean }

/** Reports browser capabilities without requesting notification permission. */
export function webPushEnvironment(): WebPushEnvironment {
  const ios = /iPad|iPhone|iPod/.test(navigator.userAgent)
  const installed =
    (typeof window.matchMedia === 'function' && window.matchMedia('(display-mode: standalone)').matches) ||
    (navigator as NavigatorWithStandalone).standalone === true
  return {
    supported: 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window,
    installed,
    ios,
  }
}

/** Converts the URL-safe VAPID public key into the bytes PushManager expects. */
export function decodeBase64URL(value: string): Uint8Array<ArrayBuffer> {
  const normalized = value.replaceAll('-', '+').replaceAll('_', '/')
  const padded = normalized.padEnd(normalized.length + ((4 - (normalized.length % 4)) % 4), '=')
  const binary = atob(padded)
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index)
  return bytes
}

export async function currentWebPushSubscription(): Promise<PushSubscription | null> {
  if (!webPushEnvironment().supported) return null
  const registration = await navigator.serviceWorker.ready
  return registration.pushManager.getSubscription()
}

/** Must be called directly from a user gesture on iOS. */
export async function subscribeBrowser(publicKey: string): Promise<PushSubscription> {
  let permission = Notification.permission
  if (permission === 'default') {
    // Keep this invocation before any await: WebKit requires the prompt to be
    // initiated by the user's button gesture.
    const permissionRequest = Notification.requestPermission()
    permission = await permissionRequest
  }
  if (permission !== 'granted') throw new Error('Notification permission was not granted.')

  const registration = await navigator.serviceWorker.ready
  const current = await registration.pushManager.getSubscription()
  if (current) return current
  return registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: decodeBase64URL(publicKey) })
}

export async function registerServiceWorker(): Promise<ServiceWorkerRegistration | null> {
  if (!('serviceWorker' in navigator)) return null
  return navigator.serviceWorker.register('/sw.js', { scope: '/' })
}
