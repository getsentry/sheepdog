import Foundation
import Testing
@testable import ReviewQueue

@Test func modelIdsAreParsedInOrder() throws {
    let data = Data("""
    { "data": [
      { "id": "google/gemini-4-hydrogen", "name": "Google: Gemini 4 Hydrogen", "context_length": 1048576 },
      { "id": "anthropic/claude-sonnet-5.5", "name": "Anthropic: Claude Sonnet 5.5" }
    ] }
    """.utf8)
    #expect(try parseModels(data) == ["google/gemini-4-hydrogen", "anthropic/claude-sonnet-5.5"])
}
