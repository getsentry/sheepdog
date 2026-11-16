import SwiftUI

struct SettingsView: View {
    @State private var apiKey = ""
    // nil until the key is read from the Keychain.
    @State private var savedKey: String?
    @State private var keychainError: String?
    @AppStorage("classificationModel") private var model = ""
    @State private var models: [String]?
    @State private var modelsError: String?
    @State private var isPickerOpen = false

    var body: some View {
        Form {
            SecureField("OpenRouter API key", text: $apiKey, prompt: Text("sk-or-v1-…"))
            if let keychainError {
                Text(keychainError)
                    .foregroundStyle(.red)
            }
            LabeledContent("Classification model") {
                Button {
                    isPickerOpen.toggle()
                } label: {
                    HStack {
                        Text(model.isEmpty ? "Select a model" : model)
                        Image(systemName: "chevron.down")
                            .font(.caption)
                    }
                }
                .disabled(apiKey.isEmpty)
                .popover(isPresented: $isPickerOpen, arrowEdge: .bottom) {
                    SearchList(prompt: "Search models", items: models, error: modelsError) { id in
                        model = id
                        isPickerOpen = false
                    }
                    .task { await loadModels() }
                }
            }
        }
        .formStyle(.grouped)
        .frame(width: 480)
        .fixedSize(horizontal: false, vertical: true)
        .task {
            do {
                let key = try Keychain.read(OpenRouter.keyAccount) ?? ""
                savedKey = key
                apiKey = key
            } catch {
                keychainError = error.localizedDescription
            }
        }
        .task(id: apiKey) {
            models = nil
            modelsError = nil
            guard let savedKey, apiKey != savedKey else { return }
            try? await Task.sleep(for: .milliseconds(500))
            guard !Task.isCancelled else { return }
            Keychain.save(apiKey, for: OpenRouter.keyAccount)
            self.savedKey = apiKey
        }
    }

    private func loadModels() async {
        guard models == nil else { return }
        modelsError = nil
        do {
            models = try await OpenRouter.models(apiKey: apiKey)
        } catch {
            modelsError = error.localizedDescription
        }
    }
}
