import { describe, expect, it, vi } from 'vitest'
import { decodeBase64URL, webPushEnvironment } from './web-push'

describe('decodeBase64URL', () => {
  it('decodes unpadded URL-safe bytes', () => {
    expect([...decodeBase64URL('SGVsbG8td29ybGQ')]).toEqual([...new TextEncoder().encode('Hello-world')])
  })
})

describe('webPushEnvironment', () => {
  it('recognises an installed iPhone web app', () => {
    vi.stubGlobal('PushManager', class {})
    vi.stubGlobal('Notification', class {})
    Object.defineProperty(navigator, 'userAgent', { configurable: true, value: 'Mozilla/5.0 (iPhone)' })
    Object.defineProperty(navigator, 'serviceWorker', { configurable: true, value: {} })
    Object.defineProperty(navigator, 'standalone', { configurable: true, value: true })
    expect(webPushEnvironment()).toEqual({ supported: true, installed: true, ios: true })
    vi.unstubAllGlobals()
  })
})
