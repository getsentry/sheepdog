import Foundation
import Testing
@testable import ReviewQueue

private let start = Date(timeIntervalSince1970: 1_800_000_000)

private func task(_ key: String) -> API.RunningTask {
    API.RunningTask(id: "T1", kind: "review", key: key, title: "AI review of #6773", subtitle: "getsentry/sentry-react-native", startedAt: start)
}

private let review = API.Review(
    repo: "getsentry/sentry-react-native", number: 6773, headSha: "03da17f", model: "google/gemini-4-hydrogen",
    risk: .yellow, summary: "Updates the JavaScript SDK.", findings: [], diffTruncated: false, prUpdatedAt: start, createdAt: start
)

@MainActor
@Test func eventsUpdateTheModel() {
    let model = AppModel()
    let repo = "getsentry/sentry-react-native"
    model.apply(.requests(API.Requests(repo: repo, fetchedAt: start, rows: [], error: nil)))
    #expect(model.repo == repo)
    #expect(model.requests?.fetchedAt == start)

    model.apply(.tasks([task("\(repo)#6773")]))
    #expect(model.isReviewing(repo: repo, number: 6773))
    #expect(!model.isReviewing(repo: repo, number: 1))

    model.apply(.review(API.ReviewOutcome(repo: repo, number: 6773, review: nil, error: "Invalid API key")))
    #expect(model.reviewErrors["\(repo)#6773"] == "Invalid API key")
    #expect(model.reviews.isEmpty)

    model.apply(.review(API.ReviewOutcome(repo: repo, number: 6773, review: review, error: nil)))
    model.apply(.tasks([]))
    #expect(model.reviews["\(repo)#6773"] == review)
    #expect(model.reviewErrors.isEmpty)
    #expect(!model.isReviewing(repo: repo, number: 6773))
}

@MainActor
@Test func serverAddressNeedsSchemeAndHost() {
    #expect(AppModel.serverAddress(" http://127.0.0.1:8080 ")?.absoluteString == "http://127.0.0.1:8080")
    #expect(AppModel.serverAddress("https://reviews.example.com/")?.host() == "reviews.example.com")
    #expect(AppModel.serverAddress("127.0.0.1:8080") == nil)
    #expect(AppModel.serverAddress("ftp://example.com") == nil)
    #expect(AppModel.serverAddress("") == nil)
}
