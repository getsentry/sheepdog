import Foundation
import Testing
@testable import ReviewQueue

private let input = ReviewInput(
    repo: "acme/app", number: 7, title: "Fix crash",
    baseRefName: "main", headRefName: "fix/crash",
    body: "", agents: nil, diff: "diff --git a/x b/x\n+fixed"
)

@Test func promptContainsAllParts() {
    let prompt = ReviewPrompt.user(input)
    #expect(!prompt.truncated)
    #expect(prompt.text.contains("Pull request #7: Fix crash"))
    #expect(prompt.text.contains("The repository has no AGENTS.md."))
    #expect(prompt.text.contains("No description."))
    #expect(prompt.text.contains("+fixed"))
}

@Test func largeDiffIsTruncated() {
    let big = ReviewInput(
        repo: "acme/app", number: 7, title: "Big", baseRefName: "main", headRefName: "big",
        body: "Body", agents: "Use tabs.", diff: String(repeating: "x", count: ReviewPrompt.maxDiffCharacters + 10)
    )
    let prompt = ReviewPrompt.user(big)
    #expect(prompt.truncated)
    #expect(prompt.text.contains("truncated=\"true\""))
    #expect(prompt.text.contains("Use tabs."))
    #expect(!prompt.text.contains(String(repeating: "x", count: ReviewPrompt.maxDiffCharacters + 1)))
}

@Test func requestUsesStrictJsonSchema() throws {
    let body = try ReviewPrompt.requestBody(model: "acme/model", user: "hi")
    let json = try #require(JSONSerialization.jsonObject(with: body) as? [String: Any])
    #expect(json["model"] as? String == "acme/model")
    let format = try #require(json["response_format"] as? [String: Any])
    #expect(format["type"] as? String == "json_schema")
    #expect((format["json_schema"] as? [String: Any])?["strict"] as? Bool == true)
    #expect((json["provider"] as? [String: Any])?["require_parameters"] as? Bool == true)
}

@Test func reviewIsParsedFromCompletion() throws {
    let content = #"{"risk":"yellow","summary":"Changes the cache.","findings":[{"severity":"high","file":"Sources/Cache.swift","title":"Race","detail":"Two threads write."}]}"#
    let completion = try JSONSerialization.data(withJSONObject: ["choices": [["message": ["content": content]]]])
    let review = try parseReview(completion)
    #expect(review.risk == .yellow)
    #expect(review.findings.first?.severity == .high)
    #expect(review.findings.first?.file == "Sources/Cache.swift")
}

@Test func completionErrorIsReported() {
    let data = Data(#"{"error":{"message":"No endpoints found","code":404}}"#.utf8)
    #expect(throws: OpenRouterError.self) { try parseReview(data) }
}
