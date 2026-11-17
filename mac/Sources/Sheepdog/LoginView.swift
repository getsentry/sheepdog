import AppKit
import SwiftUI

struct LoginView: View {
    @Environment(AppModel.self) private var model
    @State private var server = ""
    @State private var token = ""
    @State private var isSigningIn = false

    private static let newTokenURL = URL(string: "https://github.com/settings/tokens/new?scopes=repo,read:org&description=Review%20Queue")!

    var body: some View {
        switch model.auth {
        case .checking, .signedIn:
            ProgressView()
        case .signedOut:
            form
        }
    }

    private var form: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Sign in to Sheepdog")
                .font(.title2.bold())
            Form {
                TextField("Server address", text: $server, prompt: Text("http://127.0.0.1:8080"))
                SecureField("GitHub token", text: $token, prompt: Text("ghp_…"))
                    .onSubmit(signIn)
            }
            Text("Use a classic personal access token with the scopes repo and read:org. If your organization uses SAML single sign-on, click Configure SSO next to the token on GitHub and authorize it for the organization.")
                .font(.callout)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if let error = model.signInError {
                Text(error)
                    .foregroundStyle(.red)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Button("Create token on GitHub") { NSWorkspace.shared.open(Self.newTokenURL) }
                Spacer()
                if isSigningIn {
                    ProgressView()
                        .controlSize(.small)
                }
                Button("Sign in", action: signIn)
                    .keyboardShortcut(.defaultAction)
                    .disabled(isSigningIn || server.isEmpty || token.isEmpty)
            }
        }
        .padding(24)
        .frame(width: 460)
        .onAppear { server = model.serverURL }
    }

    private func signIn() {
        guard !isSigningIn, !server.isEmpty, !token.isEmpty else { return }
        isSigningIn = true
        Task {
            await model.signIn(server: server, githubToken: token)
            isSigningIn = false
            if model.auth == .signedIn { token = "" }
        }
    }
}
