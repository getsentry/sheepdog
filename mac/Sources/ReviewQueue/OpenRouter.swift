import Foundation
import Security

enum Keychain {
    private static let service = "com.alwx.reviewqueue"

    static func read(_ account: String) -> String? {
        let query: [CFString: Any] = [
            kSecClass: kSecClassGenericPassword,
            kSecAttrService: service,
            kSecAttrAccount: account,
            kSecReturnData: true,
        ]
        var result: AnyObject?
        guard SecItemCopyMatching(query as CFDictionary, &result) == errSecSuccess, let data = result as? Data else { return nil }
        return String(decoding: data, as: UTF8.self)
    }

    static func save(_ value: String, for account: String) {
        let query: [CFString: Any] = [
            kSecClass: kSecClassGenericPassword,
            kSecAttrService: service,
            kSecAttrAccount: account,
        ]
        SecItemDelete(query as CFDictionary)
        guard !value.isEmpty else { return }
        var item = query
        item[kSecValueData] = Data(value.utf8)
        SecItemAdd(item as CFDictionary, nil)
    }
}

struct OpenRouterError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

enum OpenRouter {
    static let keyAccount = "openrouter-api-key"
    private static let base = URL(string: "https://openrouter.ai/api/v1/")!

    // The models endpoint answers with a full list even for a wrong key, so the key is checked first.
    static func models(apiKey: String) async throws -> [String] {
        _ = try await get("key", apiKey: apiKey)
        return try parseModels(try await get("models/user", apiKey: apiKey))
    }

    private static func get(_ path: String, apiKey: String) async throws -> Data {
        var request = URLRequest(url: base.appending(path: path))
        request.setValue("Bearer \(apiKey)", forHTTPHeaderField: "Authorization")
        let (data, response) = try await URLSession.shared.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard status != 401 else { throw OpenRouterError(message: "Invalid API key") }
        guard (200..<300).contains(status) else { throw OpenRouterError(message: "OpenRouter returned HTTP \(status)") }
        return data
    }
}

func parseModels(_ data: Data) throws -> [String] {
    struct Response: Decodable {
        struct Model: Decodable { let id: String }
        let data: [Model]
    }
    return try JSONDecoder().decode(Response.self, from: data).data.map(\.id)
}
