import Foundation
import Testing
@testable import ReviewQueue

@Test func detailIsDecodedFromGhOutput() throws {
    let detail = try PRDetail.decode(Data("""
    {
      "body": "Fixes it",
      "headRefName": "fix/crash",
      "baseRefName": "main",
      "changedFiles": 2,
      "createdAt": "2026-09-29T01:10:35Z",
      "reviewDecision": "REVIEW_REQUIRED",
      "labels": [{ "id": "L1", "name": "dependencies", "color": "0366d6" }],
      "reviewRequests": [
        { "__typename": "User", "login": "lucas" },
        { "__typename": "Team", "name": "Mobile", "slug": "acme/mobile" }
      ],
      "latestReviews": [{ "author": { "login": "sentry" }, "state": "COMMENTED" }],
      "statusCheckRollup": [{ "__typename": "CheckRun", "status": "COMPLETED", "conclusion": "SUCCESS" }]
    }
    """.utf8))
    #expect(detail.reviewRequests.map(\.displayName) == ["lucas", "Mobile"])
    #expect(detail.labels.map(\.name) == ["dependencies"])
    #expect(detail.latestReviews.first?.state == "COMMENTED")
    #expect(detail.changedFiles == 2)
}

@Test func checksAreSummarized() {
    let checks: [PRDetail.Check] = [
        .init(status: "COMPLETED", conclusion: "SUCCESS", state: nil),
        .init(status: "COMPLETED", conclusion: "SKIPPED", state: nil),
        .init(status: "COMPLETED", conclusion: "FAILURE", state: nil),
        .init(status: "IN_PROGRESS", conclusion: "", state: nil),
        .init(status: nil, conclusion: nil, state: "PENDING"),
        .init(status: nil, conclusion: nil, state: "ERROR"),
        .init(status: nil, conclusion: nil, state: "SUCCESS"),
    ]
    let summary = summarize(checks)
    #expect(summary == CheckSummary(passed: 2, failed: 2, pending: 2, skipped: 1))
    #expect(summary.text == "2 passed, 2 failed, 2 pending, 1 skipped")
    #expect(CheckSummary(passed: 4).text == "4 passed")
}

@Test func bodyDropsHtmlComments() {
    let body = renderBody("<!-- template\nhint -->\nUse **this**")
    #expect(String(body.characters) == "Use this")
}
