import Foundation
import Testing
@testable import Sheepdog

@Test func checksTextListsNonZeroCounts() {
    let checks = API.PullRequest.Checks(passed: 23, failed: 10, pending: 0, skipped: 34)
    #expect(checks.text == "23 passed, 10 failed, 34 skipped")
    #expect(checks.total == 67)
    #expect(API.PullRequest.Checks(passed: 4, failed: 0, pending: 0, skipped: 0).text == "4 passed")
    #expect(API.PullRequest.Checks(passed: 0, failed: 0, pending: 0, skipped: 0).total == 0)
}

private func plain(_ lines: [BodyLine]) -> [String] {
    lines.map { String($0.text.characters) }
}

@Test func bodyDropsHtmlComments() {
    #expect(plain(bodyLines("<!-- template\nhint -->\nUse **this**")) == ["Use this"])
}

@Test func bodyIsSplitIntoLines() {
    let lines = bodyLines("""
    ## Changelog

    - one **bold**


    ```js
    let a = **1**
    ```
    plain
    """)
    #expect(lines.map(\.kind) == [.heading(level: 2), .gap, .text, .gap, .code, .text])
    #expect(plain(lines) == ["Changelog", "", "- one bold", "", "let a = **1**", "plain"])
}

@Test func emptyBodyHasNoLines() {
    #expect(bodyLines("<!-- only a comment -->\n\n").isEmpty)
}

private let ciScore = API.CIClassification(source: "ci", score: 2, reason: "Patch update.", model: "repo/model", headSha: "sha1")

private func stored(score: Int, prUpdatedAt: Date) -> API.Classification {
    API.Classification(
        repo: "acme/app", number: 1, headSha: "old", model: "user/model", score: score, reason: "Stored.",
        diffTruncated: false, prUpdatedAt: prUpdatedAt, createdAt: prUpdatedAt
    )
}

@Test func ciScoreComesBeforeTheAppClassification() throws {
    let row = laneRow(1, lane: nil, classification: ciScore)
    let shown = try #require(ShownClassification(row: row, stored: stored(score: 8, prUpdatedAt: .distantPast)))
    #expect(shown == .ci(ciScore))
    #expect(shown.score == 2 && shown.reason == "Patch update.")
    #expect(!shown.isOutdated(prUpdatedAt: row.updatedAt))
}

@Test func appClassificationWithoutCIScore() throws {
    let row = laneRow(1, lane: nil)
    #expect(ShownClassification(row: row, stored: nil) == nil)

    let old = stored(score: 8, prUpdatedAt: row.updatedAt.addingTimeInterval(-60))
    let outdated = try #require(ShownClassification(row: row, stored: old))
    #expect(outdated == .app(old))
    #expect(outdated.score == 8 && outdated.isOutdated(prUpdatedAt: row.updatedAt))

    let current = try #require(ShownClassification(row: row, stored: stored(score: 3, prUpdatedAt: row.updatedAt)))
    #expect(!current.isOutdated(prUpdatedAt: row.updatedAt))
}
