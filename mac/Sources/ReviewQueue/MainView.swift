import AppKit
import SwiftUI

struct MainView: View {
    @Environment(AppModel.self) private var model

    private var rows: [API.Row] { model.requests?.rows ?? [] }

    var body: some View {
        @Bindable var model = model
        NavigationSplitView {
            Group {
                if model.requests == nil && model.settings == nil {
                    StatusList()
                } else if model.repo.isEmpty {
                    VStack(spacing: 0) {
                        ErrorBanner()
                        placeholder("Select a repository")
                    }
                } else {
                    RequestList(selection: $model.selection)
                }
            }
            .toolbar {
                ToolbarItem {
                    RepoPicker()
                }
            }
            .safeAreaInset(edge: .bottom) {
                VStack(alignment: .leading, spacing: 0) {
                    if !model.tasks.isEmpty {
                        TaskPanel()
                    }
                    HStack {
                        SettingsLink {
                            Image(systemName: "gearshape")
                        }
                        .buttonStyle(.borderless)
                        .help("Settings")
                        Spacer()
                    }
                    .padding(10)
                }
            }
            .navigationSplitViewColumnWidth(min: 360, ideal: 440)
        } detail: {
            Group {
                if let row = rows.first(where: { $0.id == model.selection }) {
                    PRDetailView(repo: model.repo, row: row)
                        .id(AppModel.key(repo: model.repo, number: row.number))
                } else {
                    placeholder("Select a pull request")
                }
            }
            // AppKit starts the toolbar background of this column a few points left of the divider, over the sidebar.
            .toolbarBackgroundVisibility(.hidden, for: .windowToolbar)
        }
        .navigationTitle(rows.first { $0.id == model.selection }?.title ?? "")
        .frame(minWidth: 820, minHeight: 480)
        .onChange(of: model.repo.lowercased()) { model.selection = nil }
        .task { await model.loadRepositories() }
    }
}

private struct TaskPanel: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Divider()
            Text("Running tasks")
                .font(.caption.bold())
                .foregroundStyle(.secondary)
            ForEach(model.tasks) { task in
                HStack(spacing: 8) {
                    ProgressView()
                        .controlSize(.small)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(task.title)
                            .lineLimit(1)
                        HStack(spacing: 4) {
                            Text(task.subtitle)
                            Text("·")
                            Text(task.startedAt, style: .timer)
                                .monospacedDigit()
                        }
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                    }
                    Spacer()
                    Button {
                        Task { await model.cancelTask(task.id) }
                    } label: {
                        Image(systemName: "stop.circle")
                    }
                    .buttonStyle(.borderless)
                    .help("Stop")
                }
            }
        }
        .padding(.horizontal, 10)
        .padding(.top, 4)
    }
}

private struct RepoPicker: View {
    @Environment(AppModel.self) private var model
    @State private var isOpen = false

    // The server keeps the name in lower case. The repository list has the name as GitHub shows it.
    private var name: String {
        model.repositories?.first { $0.caseInsensitiveCompare(model.repo) == .orderedSame } ?? model.repo
    }

    var body: some View {
        Button {
            isOpen.toggle()
        } label: {
            HStack {
                Text(model.repo.isEmpty ? "Select a repository" : name)
                    .font(.headline)
                Image(systemName: "chevron.down")
                    .font(.caption)
            }
        }
        .buttonStyle(.borderless)
        .popover(isPresented: $isOpen, arrowEdge: .bottom) {
            SearchList(prompt: "Search repositories", items: model.repositories, error: model.repositoriesError) { name in
                isOpen = false
                guard name.caseInsensitiveCompare(model.repo) != .orderedSame else { return }
                Task { await model.selectRepo(name) }
            }
        }
    }
}

// The popover is a separate window, so its focus state must live inside the popover content.
struct SearchList: View {
    let prompt: String
    // nil while the items load.
    let items: [String]?
    var error: String?
    let select: (String) -> Void
    @State private var search = ""
    @FocusState private var searchFocused: Bool

    private var matches: [String] {
        let items = items ?? []
        return search.isEmpty ? items : items.filter { $0.localizedCaseInsensitiveContains(search) }
    }

    var body: some View {
        VStack(spacing: 8) {
            TextField(prompt, text: $search)
                .textFieldStyle(.roundedBorder)
                .focused($searchFocused)
                .onSubmit {
                    if let first = matches.first { select(first) }
                }
            if let error {
                Text(error)
                    .foregroundStyle(.red)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if items == nil {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List(matches, id: \.self) { name in
                    Button {
                        select(name)
                    } label: {
                        Text(name)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                }
            }
        }
        .padding()
        .frame(width: 360, height: 400)
        .task { searchFocused = true }
    }
}

// Shown before the first answer of the server.
private struct StatusList: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        VStack(spacing: 0) {
            ErrorBanner()
            ProgressView()
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }
}

private struct ErrorBanner: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        if let error = model.connectionError ?? model.error ?? model.requests?.error {
            Text(error)
                .foregroundStyle(.white)
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(8)
                .background(.red)
        }
    }
}

private struct RequestList: View {
    @Environment(AppModel.self) private var model
    @Binding var selection: API.Row.ID?

    private var rows: [API.Row] { model.requests?.rows ?? [] }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            ErrorBanner()
            if let lastRefresh = model.requests?.fetchedAt {
                Text("Updated at \(lastRefresh, format: .dateTime.hour().minute().second())")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal)
                    .padding(.vertical, 6)
            }
            if model.requests?.fetchedAt == nil && model.error == nil && model.requests?.error == nil {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if rows.isEmpty {
                placeholder("No review requests")
            } else {
                List(rows, selection: $selection) { row in
                    RowView(row: row)
                }
                .contextMenu(forSelectionType: API.Row.ID.self) { ids in
                    Button("Open on GitHub") { open(ids) }
                } primaryAction: { ids in
                    open(ids)
                }
            }
        }
    }

    private func open(_ ids: Set<API.Row.ID>) {
        for row in rows where ids.contains(row.id) {
            NSWorkspace.shared.open(row.url)
        }
    }
}

private struct RowView: View {
    let row: API.Row

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Text(verbatim: "#\(row.number)")
                .monospacedDigit()
                .foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 4) {
                Text(row.title)
                    .lineLimit(2)
                HStack(spacing: 6) {
                    Text(row.author)
                        .foregroundStyle(.secondary)
                    if row.isDraft {
                        capsule("Draft", color: .gray)
                    }
                    ForEach(row.reasons, id: \.self) { reason in
                        capsule(reason, color: .accentColor)
                    }
                }
                .font(.caption)
            }
            Spacer()
            VStack(alignment: .trailing, spacing: 4) {
                HStack(spacing: 4) {
                    Text(verbatim: "+\(row.additions)")
                        .foregroundStyle(.green)
                    Text(verbatim: "−\(row.deletions)")
                        .foregroundStyle(.red)
                }
                .monospacedDigit()
                Text(row.updatedAt, style: .relative)
                    .foregroundStyle(.secondary)
            }
            .font(.caption)
        }
        .padding(.vertical, 4)
        .contentShape(Rectangle())
    }

}

func capsule(_ text: String, color: Color) -> some View {
    Text(text)
        .padding(.horizontal, 6)
        .padding(.vertical, 1)
        .background(color.opacity(0.2), in: Capsule())
}

func placeholder(_ text: String) -> some View {
    Text(text)
        .foregroundStyle(.secondary)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
}
