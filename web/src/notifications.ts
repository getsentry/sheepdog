// Desktop notifications of the browser. The browser keeps the choice, because it is a choice of the device.

export const notificationsKey = 'sheepdog.notifications'

export interface DesktopNotification {
  title: string
  body: string
  // One event gives one notification, also with more open tabs, because a notification replaces the one with its tag.
  tag: string
  onClick: () => void
}

export function notificationsSupported(): boolean {
  return typeof Notification !== 'undefined'
}

// True when the user turned notifications on in Settings and the browser allows them.
export function notificationsOn(): boolean {
  return notificationsSupported() && Notification.permission === 'granted' && localStorage.getItem(notificationsKey) === 'on'
}

// Asks the browser for the permission when the user turns notifications on. It returns false when the browser refuses.
export async function setNotifications(on: boolean): Promise<boolean> {
  if (!on) {
    localStorage.removeItem(notificationsKey)
    return true
  }
  if (!notificationsSupported() || (await Notification.requestPermission()) !== 'granted') return false
  localStorage.setItem(notificationsKey, 'on')
  return true
}

export function showNotification({ title, body, tag, onClick }: DesktopNotification) {
  if (!notificationsOn()) return
  const notification = new Notification(title, { body, tag })
  notification.onclick = () => {
    window.focus()
    onClick()
    notification.close()
  }
}
