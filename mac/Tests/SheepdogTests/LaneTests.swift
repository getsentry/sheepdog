import Foundation
import Testing
@testable import Sheepdog

func laneRow(_ number: Int, lane: String?, action: String = "suggest", classification: API.CIClassification? = nil) -> API.Row {
    API.Row(
        number: number, title: "PR \(number)", url: URL(string: "https://github.com/acme/app/pull/\(number)")!, isDraft: false,
        updatedAt: Date(timeIntervalSince1970: 1_800_000_000), additions: 1, deletions: 1, author: "dependabot", reasons: ["you"],
        headSha: "sha\(number)", lane: lane.map { API.Lane(name: $0, action: action, notes: []) },
        classification: classification
    )
}

private func requests(_ rows: [API.Row], lanes: [String]) -> API.Requests {
    API.Requests(repo: "acme/app", fetchedAt: nil, rows: rows, lanes: lanes, rulesError: nil, error: nil)
}

@Test func rowsWithoutLanesStayFlat() {
    #expect(requests([laneRow(1, lane: nil), laneRow(2, lane: nil)], lanes: []).sections == nil)
    #expect(requests([], lanes: ["safe"]).sections == nil)
}

@Test func sectionsFollowTheFileOrderThenManual() throws {
    let rows = [
        laneRow(1, lane: "manual"),
        laneRow(2, lane: "quick_look"),
        laneRow(3, lane: "safe", action: API.Lane.approveAndMerge),
        laneRow(4, lane: nil),
        laneRow(5, lane: "safe", action: API.Lane.approveAndMerge),
    ]
    let sections = try #require(requests(rows, lanes: ["safe", "docs", "quick_look"]).sections)
    #expect(sections.map(\.name) == ["safe", "quick_look", "manual"])
    #expect(sections.map { $0.rows.map(\.number) } == [[3, 5], [2], [1, 4]])
    #expect(sections.map { $0.mergeable.map(\.number) } == [[3, 5], [], []])
}
