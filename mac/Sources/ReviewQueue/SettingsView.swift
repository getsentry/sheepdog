import SwiftUI

struct SettingsView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        Group {
            if model.auth == .signedIn {
                SettingsForm()
            } else {
                Text("Sign in to change the settings.")
                    .foregroundStyle(.secondary)
                    .padding(40)
            }
        }
        .frame(width: 480)
        .fixedSize(horizontal: false, vertical: true)
    }
}

// Exists only while signed in, so that no state of one user stays for the next user.
private struct SettingsForm: View {
    @Environment(AppModel.self) private var model
    @State private var apiKey = ""
    @State private var isSaving = false
    @State private var saved = false
    @State private var isPickerOpen = false

    var body: some View {
        Form {
            // The address stays fixed while signed in, so that the session token never goes to another server.
            LabeledContent("Server address", value: model.serverURL)
            LabeledContent("OpenRouter API key") {
                HStack {
                    SecureField("OpenRouter API key", text: $apiKey, prompt: Text(model.settings?.hasOpenRouterKey == true ? "Saved on the server" : "sk-or-v1-…"))
                        .labelsHidden()
                        .onSubmit(saveKey)
                    if saved {
                        Text("Saved")
                            .foregroundStyle(.secondary)
                    }
                    Button("Save", action: saveKey)
                        .disabled(apiKey.isEmpty || isSaving)
                }
            }
            LabeledContent("Classification model") {
                Button {
                    isPickerOpen.toggle()
                } label: {
                    HStack {
                        Text(model.settings.map { $0.model.isEmpty ? "Select a model" : $0.model } ?? "")
                        Image(systemName: "chevron.down")
                            .font(.caption)
                    }
                }
                .disabled(model.settings?.hasOpenRouterKey != true)
                .popover(isPresented: $isPickerOpen, arrowEdge: .bottom) {
                    SearchList(prompt: "Search models", items: model.models, error: model.modelsError) { id in
                        isPickerOpen = false
                        Task { await model.selectModel(id) }
                    }
                    .task { await model.loadModels() }
                }
            }
            if let error = model.settingsError {
                Text(error)
                    .foregroundStyle(.red)
                    .textSelection(.enabled)
            }
            HStack {
                Spacer()
                Button("Sign out on all devices") {
                    Task { await model.signOutEverywhere() }
                }
                Button("Sign out") {
                    Task { await model.signOut() }
                }
            }
        }
        .formStyle(.grouped)
        .task { await model.loadSettings() }
        .onChange(of: apiKey) { if !apiKey.isEmpty { saved = false } }
    }

    private func saveKey() {
        guard !apiKey.isEmpty, !isSaving else { return }
        isSaving = true
        Task {
            if await model.saveOpenRouterKey(apiKey) {
                apiKey = ""
                saved = true
            }
            isSaving = false
        }
    }
}
