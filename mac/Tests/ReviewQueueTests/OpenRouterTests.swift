import Foundation
import Testing
@testable import ReviewQueue

@Test func onlyModelsWithStructuredOutputsAreListed() throws {
    let data = Data("""
    { "data": [
      { "id": "google/gemini-4-hydrogen", "context_length": 1048576, "supported_parameters": ["tools", "structured_outputs"] },
      { "id": "old/no-schema", "supported_parameters": ["response_format"] },
      { "id": "old/no-parameters" },
      { "id": "anthropic/claude-sonnet-5.5", "supported_parameters": ["structured_outputs"] }
    ] }
    """.utf8)
    #expect(try parseModels(data) == ["google/gemini-4-hydrogen", "anthropic/claude-sonnet-5.5"])
}
