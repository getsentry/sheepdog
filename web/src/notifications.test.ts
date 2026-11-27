import { afterEach, describe, expect, it, vi } from 'vitest'
import { notificationsKey, notificationsOn, notificationsSupported, setNotifications, showNotification } from './notifications'
import { FakeNotification } from './test/notification'

afterEach(() => {
  vi.unstubAllGlobals()
  FakeNotification.reset()
  localStorage.clear()
})

describe('notifications', () => {
  it('are off and show nothing in a browser without them', async () => {
    expect(notificationsSupported()).toBe(false)
    expect(await setNotifications(true)).toBe(false)
    expect(notificationsOn()).toBe(false)
    expect(localStorage.getItem(notificationsKey)).toBeNull()
  })

  it('ask for the permission, keep the choice, and open the PR on a click', async () => {
    vi.stubGlobal('Notification', FakeNotification)
    const focus = vi.spyOn(window, 'focus').mockImplementation(() => {})
    const clicks: string[] = []
    showNotification({ title: 'Before', body: 'b', tag: 't', onClick: () => clicks.push('before') })
    expect(FakeNotification.shown).toHaveLength(0)

    expect(await setNotifications(true)).toBe(true)
    expect(localStorage.getItem(notificationsKey)).toBe('on')
    showNotification({
      title: 'New review request in acme/app',
      body: '#3 PR 3',
      tag: 'sheepdog-pr-acme/app#3',
      onClick: () => clicks.push('pr'),
    })
    const [shown] = FakeNotification.shown
    expect([shown.title, shown.options]).toEqual(['New review request in acme/app', { body: '#3 PR 3', tag: 'sheepdog-pr-acme/app#3' }])
    shown.onclick?.()
    expect(clicks).toEqual(['pr'])
    expect(focus).toHaveBeenCalledOnce()
    expect(shown.closed).toBe(true)

    expect(await setNotifications(false)).toBe(true)
    expect(localStorage.getItem(notificationsKey)).toBeNull()
    showNotification({ title: 'After', body: 'b', tag: 't', onClick: () => {} })
    expect(FakeNotification.shown).toHaveLength(1)
  })

  it('stay off when the browser refuses the permission', async () => {
    vi.stubGlobal('Notification', FakeNotification)
    FakeNotification.answer = 'denied'
    expect(await setNotifications(true)).toBe(false)
    expect(localStorage.getItem(notificationsKey)).toBeNull()
    expect(notificationsOn()).toBe(false)
  })
})
