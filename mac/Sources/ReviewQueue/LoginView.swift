import AppKit
import SwiftUI

struct LoginView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        VStack(spacing: 16) {
            switch model.auth {
            case .checking, .signedIn:
                ProgressView()
            case .ghMissing:
                Text(#"gh CLI not found. Install it with "brew install gh"."#)
            case .signedOut:
                Button("Sign in with GitHub") {
                    Task { await model.signIn() }
                }
                .controlSize(.large)
                if let error = model.error {
                    Text(error)
                        .foregroundStyle(.red)
                        .textSelection(.enabled)
                }
            case .pending(let code):
                Text("Enter this code on GitHub:")
                Text(code)
                    .font(.system(size: 36, weight: .semibold, design: .monospaced))
                    .textSelection(.enabled)
                HStack {
                    Button("Copy code") {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(code, forType: .string)
                    }
                    Button("Open GitHub") {
                        NSWorkspace.shared.open(URL(string: "https://github.com/login/device")!)
                    }
                    .keyboardShortcut(.defaultAction)
                }
                ProgressView()
                    .controlSize(.small)
            }
        }
        .padding()
    }
}
