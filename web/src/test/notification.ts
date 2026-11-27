// A Notification of the browser for tests. answer is the reply of the browser to a permission request.
export class FakeNotification {
  static permission: NotificationPermission = 'default'
  static answer: NotificationPermission = 'granted'
  static shown: FakeNotification[] = []
  onclick: (() => void) | null = null
  closed = false

  constructor(
    readonly title: string,
    readonly options: NotificationOptions,
  ) {
    FakeNotification.shown.push(this)
  }

  static async requestPermission(): Promise<NotificationPermission> {
    FakeNotification.permission = FakeNotification.answer
    return FakeNotification.permission
  }

  static reset() {
    FakeNotification.permission = 'default'
    FakeNotification.answer = 'granted'
    FakeNotification.shown = []
  }

  close() {
    this.closed = true
  }
}
