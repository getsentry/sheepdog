import Foundation
import Security

struct KeychainError: LocalizedError {
    let message: String
    var errorDescription: String? { message }

    init(_ action: String, _ status: OSStatus) {
        let reason = SecCopyErrorMessageString(status, nil) as String? ?? "OSStatus \(status)"
        message = "Cannot \(action) the Keychain: \(reason)"
    }
}

enum Keychain {
    private static let service = "com.alwx.reviewqueue"

    private static func query(_ account: String) -> [CFString: Any] {
        [kSecClass: kSecClassGenericPassword, kSecAttrService: service, kSecAttrAccount: account]
    }

    // nil when the item does not exist. A denied access prompt throws, so it is not shown as a missing item.
    static func read(_ account: String) throws -> String? {
        var item = query(account)
        item[kSecReturnData] = true
        var result: AnyObject?
        let status = SecItemCopyMatching(item as CFDictionary, &result)
        guard status != errSecItemNotFound else { return nil }
        guard status == errSecSuccess, let data = result as? Data else { throw KeychainError("read from", status) }
        return String(decoding: data, as: UTF8.self)
    }

    static func save(_ value: String, for account: String) throws {
        delete(account)
        var item = query(account)
        item[kSecValueData] = Data(value.utf8)
        let status = SecItemAdd(item as CFDictionary, nil)
        guard status == errSecSuccess else { throw KeychainError("write to", status) }
    }

    static func delete(_ account: String) {
        SecItemDelete(query(account) as CFDictionary)
    }
}
