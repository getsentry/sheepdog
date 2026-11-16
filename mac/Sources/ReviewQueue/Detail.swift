import Foundation

extension API.PullRequest.Checks {
    var total: Int { passed + failed + pending + skipped }

    var text: String {
        [(passed, "passed"), (failed, "failed"), (pending, "pending"), (skipped, "skipped")]
            .filter { $0.0 > 0 }
            .map { "\($0.0) \($0.1)" }
            .joined(separator: ", ")
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
