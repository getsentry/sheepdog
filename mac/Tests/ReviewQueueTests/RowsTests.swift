import Foundation
import Testing
@testable import ReviewQueue

private let fixture = Data("""
{
  "data": {
    "search": {
      "nodes": [
        {
          "number": 1,
          "title": "Direct request",
          "url": "https://github.com/acme/app/pull/1",
          "isDraft": false,
          "updatedAt": "2026-11-10T10:00:00Z",
          "author": { "login": "bob" },
          "reviewRequests": { "nodes": [
            { "requestedReviewer": { "__typename": "User", "login": "me" } },
            { "requestedReviewer": { "__typename": "User", "login": "carol" } }
          ] }
        },
        {
          "number": 2,
          "title": "Team request",
          "url": "https://github.com/acme/app/pull/2",
          "isDraft": false,
          "updatedAt": "2026-11-12T10:00:00Z",
          "author": { "login": "dave" },
          "reviewRequests": { "nodes": [
            { "requestedReviewer": { "__typename": "Team", "combinedSlug": "acme/mobile", "name": "Mobile" } }
          ] }
        },
        {
          "number": 3,
          "title": "Other team request",
          "url": "https://github.com/acme/app/pull/3",
          "isDraft": false,
          "updatedAt": "2026-11-09T10:00:00Z",
          "author": { "login": "erin" },
          "reviewRequests": { "nodes": [
            { "requestedReviewer": { "__typename": "Team", "combinedSlug": "acme/docs", "name": "Docs" } }
          ] }
        },
        {
          "number": 4,
          "title": "Draft",
          "url": "https://github.com/acme/app/pull/4",
          "isDraft": true,
          "updatedAt": "2026-11-11T10:00:00Z",
          "author": null,
          "reviewRequests": { "nodes": [
            { "requestedReviewer": { "__typename": "User", "login": "me" } }
          ] }
        },
        {
          "number": 5,
          "title": "User and team request",
          "url": "https://github.com/acme/app/pull/5",
          "isDraft": false,
          "updatedAt": "2026-11-12T12:00:00Z",
          "author": { "login": "bob" },
          "reviewRequests": { "nodes": [
            { "requestedReviewer": { "__typename": "User", "login": "me" } },
            { "requestedReviewer": { "__typename": "Team", "combinedSlug": "acme/mobile", "name": "Mobile" } },
            { "requestedReviewer": { "__typename": "Bot" } }
          ] }
        }
      ]
    },
    "repositoryOwner": {
      "teams": { "nodes": [ { "combinedSlug": "acme/mobile" }, { "combinedSlug": "acme/sdk" } ] }
    }
  }
}
""".utf8)

private func row(_ number: Int) throws -> PRRow {
    try #require(toRows(fixture, login: "me").first { $0.number == number })
}

@Test func directRequestIsLabeledYou() throws {
    #expect(try row(1).reasons == ["you"])
}

@Test func requestToMyTeamIsLabeledWithTeamName() throws {
    #expect(try row(2).reasons == ["Mobile"])
}

@Test func requestToOtherTeamHasNoLabel() throws {
    #expect(try row(3).reasons.isEmpty)
}

@Test func draftIsMarked() throws {
    let draft = try row(4)
    #expect(draft.isDraft)
    #expect(draft.author == "ghost")
    #expect(try !row(1).isDraft)
}

@Test func userAndTeamRequestHaveBothLabels() throws {
    #expect(try row(5).reasons == ["you", "Mobile"])
}

@Test func rowsAreSortedNewestFirst() throws {
    #expect(try toRows(fixture, login: "me").map(\.number) == [5, 2, 4, 1, 3])
}

@Test func fieldsAreDecoded() throws {
    let first = try row(1)
    #expect(first.title == "Direct request")
    #expect(first.author == "bob")
    #expect(first.url == URL(string: "https://github.com/acme/app/pull/1"))
    #expect(first.updatedAt == ISO8601DateFormatter().date(from: "2026-11-10T10:00:00Z"))
}

@Test func userOwnedRepositoryHasNoTeams() throws {
    let data = Data("""
    { "data": { "search": { "nodes": [] }, "repositoryOwner": {} } }
    """.utf8)
    #expect(try toRows(data, login: "me").isEmpty)
}
