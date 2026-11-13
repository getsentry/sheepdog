import AppKit
import SwiftUI

struct PRDetailView: View {
    @Environment(AppModel.self) private var model
    let repo: String
    let row: PRRow
    @State private var detail: PRDetail?
    @State private var lines: [BodyLine] = []
    @State private var error: String?

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 2) {
                VStack(alignment: .leading, spacing: 16) {
                    header
                    if let error {
                        Text(error)
                            .foregroundStyle(.red)
                            .textSelection(.enabled)
                    }
                    if let detail {
                        facts(detail)
                        switch reviewState {
                        case .done(let result):
                            ReviewSection(result: result, prUpdatedAt: row.updatedAt)
                        case .failed(let message):
                            Text(message)
                                .foregroundStyle(.red)
                                .textSelection(.enabled)
                        case .running, nil:
                            EmptyView()
                        }
                        Divider()
                        if lines.isEmpty {
                            Text("No description")
                                .foregroundStyle(.secondary)
                        }
                    } else if error == nil {
                        ProgressView()
                            .frame(maxWidth: .infinity)
                    }
                }
                .padding(.bottom, 14)
                ForEach(lines) { BodyLineView(line: $0) }
            }
            .textSelection(.enabled)
            .padding()
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task(id: row.updatedAt) { await load() }
    }

    private var reviewState: ReviewState? {
        model.reviews["\(repo)#\(row.number)"]
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                Text(verbatim: "#\(row.number)")
                    .monospacedDigit()
                Text(row.author)
                if row.isDraft {
                    capsule("Draft", color: .gray)
                }
                Spacer()
                Button {
                    guard let detail else { return }
                    Task { await model.review(repo: repo, row: row, detail: detail) }
                } label: {
                    if reviewState == .running {
                        HStack(spacing: 6) {
                            ProgressView()
                                .controlSize(.small)
                            Text("Reviewing…")
                        }
                    } else if case .done = reviewState {
                        Text("Review again")
                    } else {
                        Text("AI Review")
                    }
                }
                .disabled(detail == nil || reviewState == .running)
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
            lines = bodyLines(fetched.body)
            detail = fetched
            error = nil
        } catch {
            guard !Task.isCancelled else { return }
            self.error = error.localizedDescription
        }
    }
}

private struct ReviewSection: View {
    let result: ReviewResult
    let prUpdatedAt: Date

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                Circle()
                    .fill(color(result.review.risk))
                    .frame(width: 12, height: 12)
                Text(label(result.review.risk))
                    .font(.headline)
                Spacer()
                Text("\(result.model), \(result.date.formatted(date: .omitted, time: .shortened))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if prUpdatedAt > result.prUpdatedAt {
                Text("The PR changed after this review.")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }
            if result.diffTruncated {
                Text("The diff was too large. The model saw only the first \(ReviewPrompt.maxDiffCharacters.formatted()) characters.")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }
            Text(result.review.summary)
            ForEach(result.review.findings, id: \.self) { finding in
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        capsule(finding.severity.rawValue.capitalized, color: color(finding.severity))
                        Text(finding.title)
                            .bold()
                    }
                    Text(finding.file)
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                    Text(finding.detail)
                }
            }
        }
        .textSelection(.enabled)
        .padding(12)
        .background(color(result.review.risk).opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
    }

    private func label(_ risk: Review.Risk) -> String {
        switch risk {
        case .green: "Low risk"
        case .yellow: "Needs a careful review"
        case .red: "High risk, review in full"
        }
    }

    private func color(_ risk: Review.Risk) -> Color {
        switch risk {
        case .green: .green
        case .yellow: .yellow
        case .red: .red
        }
    }

    private func color(_ severity: Review.Finding.Severity) -> Color {
        switch severity {
        case .high: .red
        case .medium: .orange
        case .low: .gray
        }
    }
}

private struct BodyLineView: View {
    let line: BodyLine

    var body: some View {
        switch line.kind {
        case .text:
            Text(line.text)
        case .heading(let level):
            Text(line.text)
                .font(level <= 2 ? .title3.bold() : .headline)
                .padding(.top, 6)
        case .code:
            Text(line.text)
                .font(.callout.monospaced())
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 8)
                .background(.quaternary)
        case .gap:
            Spacer().frame(height: 6)
        }
    }
}

private func humanize(_ value: String) -> String {
    let words = value.lowercased().replacing("_", with: " ")
    return words.prefix(1).uppercased() + words.dropFirst()
}
