import AppKit
import SwiftUI

struct PRDetailView: View {
    let repo: String
    let row: PRRow
    @State private var detail: PRDetail?
    @State private var error: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                header
                if let error {
                    Text(error)
                        .foregroundStyle(.red)
                        .textSelection(.enabled)
                }
                if let detail {
                    facts(detail)
                    Divider()
                    if detail.body.isEmpty {
                        Text("No description")
                            .foregroundStyle(.secondary)
                    } else {
                        Text(renderBody(detail.body))
                            .textSelection(.enabled)
                    }
                } else if error == nil {
                    ProgressView()
                        .frame(maxWidth: .infinity)
                }
            }
            .padding()
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task(id: row.updatedAt) { await load() }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(row.title)
                .font(.title2.bold())
                .textSelection(.enabled)
            HStack(spacing: 6) {
                Text(verbatim: "#\(row.number)")
                    .monospacedDigit()
                Text(row.author)
                if row.isDraft {
                    capsule("Draft", color: .gray)
                }
                Spacer()
                Button("Open on GitHub") { NSWorkspace.shared.open(row.url) }
            }
            .foregroundStyle(.secondary)
            if let detail {
                Text(verbatim: "\(detail.baseRefName) ← \(detail.headRefName)")
                    .font(.callout.monospaced())
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }
        }
    }

    private func facts(_ detail: PRDetail) -> some View {
        let checks = summarize(detail.statusCheckRollup)
        return Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 12, verticalSpacing: 6) {
            fact("Created") { Text(detail.createdAt, format: .dateTime) }
            fact("Changes") {
                HStack(spacing: 4) {
                    Text(verbatim: "+\(row.additions)").foregroundStyle(.green)
                    Text(verbatim: "−\(row.deletions)").foregroundStyle(.red)
                    Text("in \(detail.changedFiles) files")
                }
                .monospacedDigit()
            }
            fact("Review") { Text(humanize(detail.reviewDecision.isEmpty ? "NONE" : detail.reviewDecision)) }
            fact("Checks") {
                if detail.statusCheckRollup.isEmpty {
                    Text("None")
                } else {
                    Text(checks.text)
                        .foregroundStyle(checks.failed > 0 ? .red : checks.pending > 0 ? .orange : .green)
                }
            }
            fact("Requested") {
                Text(detail.reviewRequests.map(\.displayName).joined(separator: ", "))
            }
            if !detail.latestReviews.isEmpty {
                fact("Reviews") {
                    VStack(alignment: .leading, spacing: 2) {
                        ForEach(detail.latestReviews, id: \.author.login) { review in
                            Text("\(review.author.login): \(humanize(review.state))")
                        }
                    }
                }
            }
            if !detail.labels.isEmpty {
                fact("Labels") {
                    HStack(spacing: 4) {
                        ForEach(detail.labels, id: \.name) { capsule($0.name, color: .accentColor) }
                    }
                }
            }
        }
    }

    private func fact(_ title: String, @ViewBuilder value: () -> some View) -> some View {
        GridRow {
            Text(title)
                .foregroundStyle(.secondary)
                .gridColumnAlignment(.trailing)
            value()
        }
    }

    private func load() async {
        do {
            let fetched = try await GH.pullRequest(repo: repo, number: row.number)
            guard !Task.isCancelled else { return }
            detail = fetched
            error = nil
        } catch {
            guard !Task.isCancelled else { return }
            self.error = error.localizedDescription
        }
    }
}

private func humanize(_ value: String) -> String {
    let words = value.lowercased().replacing("_", with: " ")
    return words.prefix(1).uppercased() + words.dropFirst()
}
