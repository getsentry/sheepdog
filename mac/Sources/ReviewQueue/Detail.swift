import Foundation

struct PRDetail: Decodable, Equatable {
    struct Label: Decodable, Equatable {
        let name: String
    }

    struct Reviewer: Decodable, Equatable {
        let login: String?
        let name: String?

        var displayName: String { login ?? name ?? "unknown" }
    }

    struct Review: Decodable, Equatable {
        struct Author: Decodable, Equatable {
            let login: String
        }

        let author: Author
        let state: String
    }

    struct Check: Decodable, Equatable {
        let status: String?
        let conclusion: String?
        let state: String?
    }

    let body: String
    let headRefName: String
    let baseRefName: String
    let changedFiles: Int
    let createdAt: Date
    let reviewDecision: String
    let labels: [Label]
    let reviewRequests: [Reviewer]
    let latestReviews: [Review]
    let statusCheckRollup: [Check]

    static let fields = "body,headRefName,baseRefName,changedFiles,createdAt,reviewDecision,labels,reviewRequests,latestReviews,statusCheckRollup"

    static func decode(_ data: Data) throws -> PRDetail {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode(PRDetail.self, from: data)
    }
}

struct CheckSummary: Equatable {
    var passed = 0
    var failed = 0
    var pending = 0
    var skipped = 0

    var text: String {
        [(passed, "passed"), (failed, "failed"), (pending, "pending"), (skipped, "skipped")]
            .filter { $0.0 > 0 }
            .map { "\($0.0) \($0.1)" }
            .joined(separator: ", ")
    }
}

// A CheckRun reports status and conclusion, a StatusContext reports only state.
func summarize(_ checks: [PRDetail.Check]) -> CheckSummary {
    checks.reduce(into: CheckSummary()) { summary, check in
        let result = check.conclusion ?? check.state ?? ""
        if ["FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE"].contains(result) {
            summary.failed += 1
        } else if ["SKIPPED", "NEUTRAL"].contains(result) {
            summary.skipped += 1
        } else if (check.status ?? "COMPLETED") != "COMPLETED" || ["PENDING", "EXPECTED"].contains(result) {
            summary.pending += 1
        } else {
            summary.passed += 1
        }
    }
}

struct BodyLine: Identifiable, Equatable {
    enum Kind: Equatable {
        case text
        case heading(level: Int)
        case code
        case gap
    }

    let id: Int
    let kind: Kind
    let text: AttributedString
}

// One SwiftUI Text for a long body is drawn as one bitmap of the full text height, which takes hundreds of MB.
// Separate lines let a LazyVStack draw only the visible part.
func bodyLines(_ body: String) -> [BodyLine] {
    let options = AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace)
    let text = body.replacing(/<!--.*?-->/.dotMatchesNewlines(), with: "")
        .trimmingCharacters(in: .whitespacesAndNewlines)
    var lines: [BodyLine] = []
    var inCode = false

    func add(_ kind: BodyLine.Kind, _ value: some StringProtocol) {
        let string = String(value)
        let attributed = kind == .code
            ? AttributedString(string)
            : (try? AttributedString(markdown: string, options: options)) ?? AttributedString(string)
        lines.append(BodyLine(id: lines.count, kind: kind, text: attributed))
    }

    for raw in text.split(separator: "\n", omittingEmptySubsequences: false) {
        let line = raw.trimmingCharacters(in: .whitespaces)
        if line.hasPrefix("```") {
            inCode.toggle()
        } else if inCode {
            add(.code, raw)
        } else if line.isEmpty {
            if lines.last.map({ $0.kind != .gap }) ?? false { add(.gap, "") }
        } else if let match = line.wholeMatch(of: /(#{1,6})\s+(.*)/) {
            add(.heading(level: match.1.count), match.2)
        } else {
            add(.text, raw)
        }
    }
    return lines
}
