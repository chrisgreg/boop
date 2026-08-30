/* global self */

self.addEventListener('push', (event) => {
  let payload = {}
  try {
    payload = event.data?.json() ?? {}
  } catch {
    payload = { title: 'Boop', body: event.data?.text() ?? 'A new event arrived.' }
  }
  const title = typeof payload.title === 'string' && payload.title ? payload.title : 'Boop'
  const options = {
    body: typeof payload.body === 'string' ? payload.body : '',
    icon: typeof payload.icon === 'string' ? payload.icon : '/icons/icon-192.png',
    badge: typeof payload.badge === 'string' ? payload.badge : '/icons/badge-96.png',
    tag: typeof payload.tag === 'string' ? payload.tag : undefined,
    data: { url: typeof payload.url === 'string' ? payload.url : '/' },
  }
  event.waitUntil(self.registration.showNotification(title, options))
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const requested = event.notification.data?.url ?? '/'
  const destination = new URL(requested, self.location.origin)
  const safeURL = destination.origin === self.location.origin ? destination.href : self.location.origin
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then(async (clients) => {
      for (const client of clients) {
        if ('navigate' in client) await client.navigate(safeURL)
        return client.focus()
      }
      return self.clients.openWindow(safeURL)
    }),
  )
})
