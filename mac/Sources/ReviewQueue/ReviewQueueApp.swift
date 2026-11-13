import AppKit
import SwiftUI

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    let model = AppModel()

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }

    // A pending gh auth login would otherwise keep polling GitHub after the app quits.
    func applicationWillTerminate(_ notification: Notification) {
        model.stopLogin()
    }
}

@main
struct ReviewQueueApp: App {
    @NSApplicationDelegateAdaptor private var delegate: AppDelegate

    var body: some Scene {
        Window("Review Queue", id: "main") {
            Group {
                if case .signedIn = delegate.model.auth {
                    MainView()
                } else {
                    LoginView()
                }
            }
            .frame(minWidth: 480, minHeight: 360)
            .environment(delegate.model)
            .task { await delegate.model.start() }
        }
        Settings {
            SettingsView()
        }
    }
}
