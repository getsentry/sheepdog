import AppKit
import SwiftUI

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    let model = AppModel()

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }
}

@main
struct ReviewQueueApp: App {
    @NSApplicationDelegateAdaptor private var delegate: AppDelegate

    var body: some Scene {
        Window("Review Queue", id: "main") {
            Group {
                if delegate.model.auth == .signedIn {
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
                .environment(delegate.model)
        }
    }
}
