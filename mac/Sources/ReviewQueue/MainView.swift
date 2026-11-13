import AppKit
import SwiftUI

struct MainView: View {
    @Environment(AppModel.self) private var model
    @AppStorage("repo") private var repo = ""
    @State private var selection: PRRow.ID?

    var body: some View {
        NavigationSplitView {
            VStack(alignment: .leading, spacing: 0) {
                RepoPicker(repo: $repo)
                    .padding()
                Divider()
                if repo.isEmpty {
                    placeholder("Select a repository")
                } else {
                    RequestList(selection: $selection)
                        .task(id: repo) { await model.poll(repo: repo) }
                }
            }
            .navigationSplitViewColumnWidth(min: 360, ideal: 440)
        } detail: {
            if let row = model.rows.first(where: { $0.id == selection }) {
                PRDetailView(repo: repo, row: row)
                    .id("\(repo)#\(row.number)")
            } else {
                placeholder("Select a pull request")
            }
        }
        .navigationTitle(model.rows.first { $0.id == selection }?.title ?? "")
        .frame(minWidth: 820, minHeight: 480)
        .onChange(of: repo) { selection = nil }
        .task { await model.loadRepositories() }
    }
}

private struct RepoPicker: View {
    @Binding var repo: String
    @State private var isOpen = false

    var body: some View {
        Button {
            isOpen.toggle()
        } label: {
            HStack {
                Text(repo.isEmpty ? "Select a repository" : repo)
                    .font(.headline)
                Image(systemName: "chevron.down")
                    .font(.caption)
            }
        }
        .buttonStyle(.borderless)
        .popover(isPresented: $isOpen, arrowEdge: .bottom) {
            RepoSearch { name in
                repo = name
                isOpen = false
            }
        }
    }
}

// The popover is a separate window, so its focus state must live inside the popover content.
private struct RepoSearch: View {
    @Environment(AppModel.self) private var model
    let select: (String) -> Void
    @State private var search = ""
    @FocusState private var searchFocused: Bool

    private var matches: [String] {
        search.isEmpty ? model.repositories : model.repositories.filter { $0.localizedCaseInsensitiveContains(search) }
    }

    var body: some View {
        VStack(spacing: 8) {
            TextField("Search repositories", text: $search)
                .textFieldStyle(.roundedBorder)
                .focused($searchFocused)
                .onSubmit {
                    if let first = matches.first { select(first) }
                }
            if model.repositories.isEmpty {
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

private struct RequestList: View {
    @Environment(AppModel.self) private var model
    @Binding var selection: PRRow.ID?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if let error = model.error {
                Text(error)
                    .foregroundStyle(.white)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(8)
                    .background(.red)
            }
            if let lastRefresh = model.lastRefresh {
                Text("Updated at \(lastRefresh, format: .dateTime.hour().minute().second())")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal)
                    .padding(.vertical, 6)
            }
            if model.lastRefresh == nil && model.error == nil {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if model.rows.isEmpty {
                placeholder("No review requests")
            } else {
                List(model.rows, selection: $selection) { row in
                    RowView(row: row)
                }
                .contextMenu(forSelectionType: PRRow.ID.self) { ids in
                    Button("Open on GitHub") { open(ids) }
                } primaryAction: { ids in
                    open(ids)
                }
            }
        }
    }

    private func open(_ ids: Set<PRRow.ID>) {
        for row in model.rows where ids.contains(row.id) {
            NSWorkspace.shared.open(row.url)
        }
    }
}

private struct RowView: View {
    let row: PRRow

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
