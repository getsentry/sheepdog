import Foundation
import Synchronization
@testable import Sheepdog

struct Reply: Sendable {
    let status: Int
    let body: String
}

struct Seen: Sendable {
    let method: String
    let path: String
    let authorization: String?
    let body: Data?
}

// Each test registers its own host, so that tests can run in parallel.
final class MockProtocol: URLProtocol, @unchecked Sendable {
    typealias Handler = @Sendable (Seen) async -> Reply
    private static let handlers = Mutex<[String: Handler]>([:])
    // Used only on the loading thread of this request.
    private var stopped = false
    private var work: Task<Void, Never>?

    static func server(_ handler: @escaping Handler) -> (url: URL, session: URLSession) {
        let host = "h\(UUID().uuidString.lowercased()).test"
        handlers.withLock { $0[host] = handler }
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [MockProtocol.self]
        return (URL(string: "https://\(host)")!, URLSession(configuration: configuration))
    }

    static func client(token: String?, handler: @escaping Handler, sleep: @escaping @Sendable (Duration) async throws -> Void = { _ in }) -> APIClient {
        let (url, session) = server(handler)
        return APIClient(baseURL: url, sessionToken: token, session: session, sleep: sleep)
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func stopLoading() {
        stopped = true
        work?.cancel()
    }

    // The handler can wait, so the reply goes back to the run loop of the loading thread.
    override func startLoading() {
        let url = request.url!
        let handler = Self.handlers.withLock { $0[url.host()!] }!
        let seen = Seen(method: request.httpMethod ?? "", path: url.path(), authorization: request.value(forHTTPHeaderField: "Authorization"), body: bodyData())
        let target = Unchecked(mock: self, runLoop: CFRunLoopGetCurrent())
        work = Task {
            let reply = await handler(seen)
            CFRunLoopPerformBlock(target.runLoop, CFRunLoopMode.commonModes.rawValue) { target.mock.finish(url: url, reply: reply) }
            CFRunLoopWakeUp(target.runLoop)
        }
    }

    private func finish(url: URL, reply: Reply) {
        guard !stopped else { return }
        let response = HTTPURLResponse(url: url, statusCode: reply.status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(reply.body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    // URLSession moves httpBody into a stream before the protocol sees it.
    private func bodyData() -> Data? {
        guard let stream = request.httpBodyStream else { return request.httpBody }
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            guard count > 0 else { break }
            data.append(buffer, count: count)
        }
        return data
    }
}

// The protocol and its run loop are used only on the loading thread.
private struct Unchecked: @unchecked Sendable {
    let mock: MockProtocol
    let runLoop: CFRunLoop
}

// Holds requests until the test opens it.
actor Gate {
    private var isOpen = false
    private var waiters: [CheckedContinuation<Void, Never>] = []

    func wait() async {
        if isOpen { return }
        await withCheckedContinuation { waiters.append($0) }
    }

    func open() {
        isOpen = true
        waiters.forEach { $0.resume() }
        waiters = []
    }
}

// Keeps the session token in memory, so that tests never touch the Keychain.
final class MemoryTokens: Sendable {
    private let token: Mutex<String?>

    init(_ token: String?) {
        self.token = Mutex(token)
    }

    var value: String? {
        get { token.withLock { $0 } }
        set { token.withLock { $0 = newValue } }
    }

    var store: TokenStore {
        TokenStore(read: { self.value }, save: { self.value = $0 }, delete: { self.value = nil })
    }
}

@MainActor
func until(_ condition: () -> Bool) async -> Bool {
    for _ in 0..<300 {
        if condition() { return true }
        try? await Task.sleep(for: .milliseconds(10))
    }
    return condition()
}
