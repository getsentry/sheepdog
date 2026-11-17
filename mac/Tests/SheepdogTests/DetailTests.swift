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
