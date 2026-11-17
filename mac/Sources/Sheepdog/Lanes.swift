import SwiftUI

struct LaneSection: Identifiable, Equatable {
    let name: String
    let rows: [API.Row]

    var id: String { name }
    var mergeable: [API.Row] { rows.filter(\.canApproveAndMerge) }
}

extension API.Requests {
    // nil when no row has a lane, so that the list stays flat. A row without a lane, from a base branch without rules, is in manual.
    var sections: [LaneSection]? {
        guard rows.contains(where: { $0.lane != nil }) else { return nil }
        return (lanes + [API.Lane.manual]).compactMap { name in
            let rows = rows.filter { ($0.lane?.name ?? API.Lane.manual) == name }
            return rows.isEmpty ? nil : LaneSection(name: name, rows: rows)
        }
    }
}

struct MergeBatch: Identifiable {
    let rows: [API.Row]

    var id: [Int] { rows.map(\.number) }
}

struct MergeConfirmation: View {
    @Environment(\.dismiss) private var dismiss
    let batch: MergeBatch
    let confirm: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(batch.rows.count == 1 ? "Approve and merge this pull request?" : "Approve and merge \(batch.rows.count) pull requests?")
                .font(.headline)
            Text("Sheepdog approves each pull request with your GitHub account. Then it merges the pull request, or turns on auto-merge if GitHub cannot merge it yet.")
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            ScrollView {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(batch.rows) { row in
                        HStack(alignment: .firstTextBaseline, spacing: 6) {
                            Text(verbatim: "#\(row.number)")
                                .monospacedDigit()
                                .foregroundStyle(.secondary)
                            Text(row.title)
                        }
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(maxHeight: 240)
            .fixedSize(horizontal: false, vertical: true)
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Approve and merge") {
                    dismiss()
                    confirm()
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 440)
    }
}
