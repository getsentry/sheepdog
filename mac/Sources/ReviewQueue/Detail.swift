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

// PR templates leave HTML comments in the body, and they are noise in the panel.
func renderBody(_ body: String) -> AttributedString {
    let text = body.replacing(/<!--.*?-->/.dotMatchesNewlines(), with: "")
        .trimmingCharacters(in: .whitespacesAndNewlines)
    let options = AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace)
    return (try? AttributedString(markdown: text, options: options)) ?? AttributedString(text)
}
