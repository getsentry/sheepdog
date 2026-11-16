import Foundation
import OSLog

private let log = Logger(subsystem: "com.alwx.reviewqueue", category: "server")

// The JSON types of the Review Queue server.
enum API {
    struct Session: Decodable, Equatable, Sendable {
        let sessionToken: String
        let login: String
    }

    struct Settings: Decodable, Equatable, Sendable {
        let repo: String
        let model: String
        let hasOpenRouterKey: Bool
    }

    // The result of the last poll. fetchedAt is nil before the first successful poll.
    struct Requests: Decodable, Equatable, Sendable {
        let repo: String
        let fetchedAt: Date?
        let rows: [Row]
        let error: String?
    }

    struct Row: Decodable, Equatable, Identifiable, Sendable {
        let number: Int
        let title: String
        let url: URL
        let isDraft: Bool
        let updatedAt: Date
        let additions: Int
        let deletions: Int
        let author: String
        let reasons: [String]

        var id: Int { number }
    }

    struct PullRequest: Decodable, Equatable, Sendable {
        struct LatestReview: Decodable, Equatable, Sendable {
            let author: String
            let state: String
        }

        struct Checks: Decodable, Equatable, Sendable {
            let passed: Int
            let failed: Int
            let pending: Int
            let skipped: Int
        }

        let title: String
        let body: String
        let headRefName: String
        let baseRefName: String
        let headRefOid: String
        let changedFiles: Int
        let createdAt: Date
        let updatedAt: Date
        let reviewDecision: String
        let labels: [String]
        let reviewRequests: [String]
        let latestReviews: [LatestReview]
        let checks: Checks
    }

    struct Review: Decodable, Equatable, Sendable {
        enum Risk: String, Decodable, Sendable {
            case green, yellow, red
        }

        struct Finding: Decodable, Equatable, Hashable, Sendable {
            enum Severity: String, Decodable, Sendable {
                case high, medium, low
            }

            let severity: Severity
            let file: String
            let title: String
            let detail: String
        }

        let repo: String
        let number: Int
        let headSha: String
        let model: String
        let risk: Risk
        let summary: String
        let findings: [Finding]
        let diffTruncated: Bool
        let prUpdatedAt: Date
        let createdAt: Date
    }

    struct RunningTask: Decodable, Equatable, Identifiable, Sendable {
        let id: String
        let kind: String
        let key: String
        let title: String
        let subtitle: String
        let startedAt: Date
    }

    // The end of a review task. A cancelled review sends no outcome.
    struct ReviewOutcome: Decodable, Equatable, Sendable {
        let repo: String
        let number: Int
        let review: Review?
        let error: String?
    }

    enum Event: Equatable, Sendable {
        case requests(Requests)
        case tasks([RunningTask])
        case review(ReviewOutcome)

        // nil for an event name that this client does not know.
        init?(_ message: SSEMessage) throws {
            let data = Data(message.data.utf8)
            switch message.event {
            case "requests": self = .requests(try API.decode(Requests.self, from: data))
            case "tasks": self = .tasks(try API.decode([RunningTask].self, from: data))
            case "review": self = .review(try API.decode(ReviewOutcome.self, from: data))
            default: return nil
            }
        }
    }

    // Go writes times as RFC 3339 with up to nine fraction digits, or with none. Older Foundation versions parse no fractions with plain .iso8601.
    static func decode<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let container = try decoder.singleValueContainer()
            let text = try container.decode(String.self)
            if let date = try? Date(text, strategy: Date.ISO8601FormatStyle(includingFractionalSeconds: true)) {
                return date
            }
            if let date = try? Date(text, strategy: .iso8601) {
                return date
            }
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "The date \(text) is not RFC 3339")
        }
        return try decoder.decode(type, from: data)
    }
}

enum APIError: LocalizedError, Equatable {
    // The session or the GitHub token is not valid. The client must sign in again.
    case unauthorized(String)
    // The GitHub account is no longer on the allowlist of the server.
    case forbidden(String)
    case server(status: Int, message: String)

    init(status: Int, data: Data) {
        struct Failure: Decodable { let error: String }
        let message = (try? JSONDecoder().decode(Failure.self, from: data))?.error ?? "The server returned HTTP \(status)"
        switch status {
        case 401: self = .unauthorized(message)
        case 403: self = .forbidden(message)
        default: self = .server(status: status, message: message)
        }
    }

    var endsSession: Bool {
        switch self {
        case .unauthorized, .forbidden: true
        case .server: false
        }
    }

    var errorDescription: String? {
        switch self {
        case .unauthorized(let message), .forbidden(let message), .server(_, let message): message
        }
    }
}

enum StreamUpdate: Equatable, Sendable {
    case connected
    case event(API.Event)
    // The stream failed and connects again after a delay.
    case disconnected(String)
}

struct APIClient: Sendable {
    let baseURL: URL
    let sessionToken: String?
    private let session: URLSession
    private let sleep: @Sendable (Duration) async throws -> Void

    init(
        baseURL: URL,
        sessionToken: String? = nil,
        session: URLSession = .shared,
        sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }
    ) {
        self.baseURL = baseURL
        self.sessionToken = sessionToken
        self.session = session
        self.sleep = sleep
    }

    func signIn(githubToken: String) async throws -> API.Session {
        let body = try JSONEncoder().encode(["githubToken": githubToken])
        return try API.decode(API.Session.self, from: try await send("POST", "api/session", body: body))
    }

    func signOut() async throws {
        _ = try await send("DELETE", "api/session")
    }

    func signOutEverywhere() async throws {
        _ = try await send("DELETE", "api/sessions")
    }

    func me() async throws -> String {
        struct Me: Decodable { let login: String }
        return try API.decode(Me.self, from: try await send("GET", "api/me")).login
    }

    func settings() async throws -> API.Settings {
        try API.decode(API.Settings.self, from: try await send("GET", "api/settings"))
    }

    // A nil value stays unchanged. An empty openRouterKey deletes the key.
    func updateSettings(repo: String? = nil, model: String? = nil, openRouterKey: String? = nil) async throws -> API.Settings {
        struct Update: Encodable {
            let repo: String?
            let model: String?
            let openRouterKey: String?
        }
        let body = try JSONEncoder().encode(Update(repo: repo, model: model, openRouterKey: openRouterKey))
        return try API.decode(API.Settings.self, from: try await send("PUT", "api/settings", body: body))
    }

    func repos() async throws -> [String] {
        try API.decode([String].self, from: try await send("GET", "api/repos"))
    }

    func models() async throws -> [String] {
        try API.decode([String].self, from: try await send("GET", "api/models"))
    }

    func reviewRequests() async throws -> API.Requests {
        try API.decode(API.Requests.self, from: try await send("GET", "api/review-requests"))
    }

    // repo is "owner/name".
    func pullRequest(repo: String, number: Int) async throws -> API.PullRequest {
        try API.decode(API.PullRequest.self, from: try await send("GET", "api/repos/\(repo)/pulls/\(number)"))
    }

    // nil when the PR has no stored review.
    func review(repo: String, number: Int) async throws -> API.Review? {
        do {
            return try API.decode(API.Review.self, from: try await send("GET", "api/repos/\(repo)/pulls/\(number)/review"))
        } catch APIError.server(status: 404, _) {
            return nil
        }
    }

    func startReview(repo: String, number: Int) async throws -> API.RunningTask {
        try API.decode(API.RunningTask.self, from: try await send("POST", "api/repos/\(repo)/pulls/\(number)/review"))
    }

    func tasks() async throws -> [API.RunningTask] {
        try API.decode([API.RunningTask].self, from: try await send("GET", "api/tasks"))
    }

    func cancelTask(id: String) async throws {
        _ = try await send("DELETE", "api/tasks/\(id)")
    }

    static func reconnectDelay(attempt: Int) -> Duration {
        [.seconds(2), .seconds(4), .seconds(8)].dropFirst(attempt).first ?? .seconds(30)
    }

    // The stream connects again after a drop and ends with an error on 401 or 403.
    func events() -> AsyncThrowingStream<StreamUpdate, any Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                var attempt = 0
                while !Task.isCancelled {
                    var delay: Duration?
                    do {
                        try await readEvents(into: continuation, attempt: &attempt)
                    } catch let error as APIError where error.endsSession {
                        continuation.finish(throwing: error)
                        return
                    } catch {
                        guard !Task.isCancelled else { break }
                        continuation.yield(.disconnected(error.localizedDescription))
                        // Too many open streams: a fast retry cannot succeed before another client closes its stream.
                        if case APIError.server(status: 429, _) = error { delay = .seconds(30) }
                    }
                    do {
                        try await sleep(delay ?? Self.reconnectDelay(attempt: attempt))
                    } catch {
                        break
                    }
                    attempt += 1
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    private func readEvents(into continuation: AsyncThrowingStream<StreamUpdate, any Error>.Continuation, attempt: inout Int) async throws {
        var request = makeRequest("GET", "api/events")
        request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
        let (bytes, response) = try await session.bytes(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard status == 200 else {
            var data = Data()
            for try await byte in bytes { data.append(byte) }
            throw APIError(status: status, data: data)
        }
        continuation.yield(.connected)
        var parser = SSEParser()
        for try await byte in bytes {
            guard let message = parser.push(byte) else { continue }
            let event: API.Event?
            do {
                event = try API.Event(message)
            } catch {
                log.error("skip a \(message.event, privacy: .public) event that does not decode: \(error.localizedDescription, privacy: .public)")
                continue
            }
            guard let event else { continue }
            attempt = 0
            continuation.yield(.event(event))
        }
    }

    private func makeRequest(_ method: String, _ path: String) -> URLRequest {
        var request = URLRequest(url: baseURL.appending(path: path))
        request.httpMethod = method
        if let sessionToken {
            request.setValue("Bearer \(sessionToken)", forHTTPHeaderField: "Authorization")
        }
        return request
    }

    private func send(_ method: String, _ path: String, body: Data? = nil) async throws -> Data {
        var request = makeRequest(method, path)
        if let body {
            request.httpBody = body
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let (data, response) = try await session.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else { throw APIError(status: status, data: data) }
        return data
    }
}

struct SSEMessage: Equatable, Sendable {
    let event: String
    let data: String
}

// Reads Server-Sent Events one byte at a time, so that a line or an event can arrive in several reads.
struct SSEParser {
    private var line: [UInt8] = []
    private var afterCR = false
    private var event = ""
    private var data: [String] = []

    mutating func push(_ byte: UInt8) -> SSEMessage? {
        let wasCR = afterCR
        afterCR = byte == 0x0D
        switch byte {
        case 0x0A where wasCR: return nil
        case 0x0A, 0x0D: return endLine()
        default:
            line.append(byte)
            return nil
        }
    }

    mutating func push(_ bytes: some Sequence<UInt8>) -> [SSEMessage] {
        var messages: [SSEMessage] = []
        for byte in bytes {
            if let message = push(byte) { messages.append(message) }
        }
        return messages
    }

    private mutating func endLine() -> SSEMessage? {
        let text = String(decoding: line, as: UTF8.self)
        line.removeAll(keepingCapacity: true)
        if text.isEmpty { return dispatch() }
        if text.hasPrefix(":") { return nil }
        let field = text.prefix { $0 != ":" }
        var value = text.dropFirst(field.count).dropFirst()
        if value.hasPrefix(" ") { value = value.dropFirst() }
        switch field {
        case "event": event = String(value)
        case "data": data.append(String(value))
        default: break
        }
        return nil
    }

    private mutating func dispatch() -> SSEMessage? {
        defer {
            event = ""
            data = []
        }
        guard !data.isEmpty else { return nil }
        return SSEMessage(event: event.isEmpty ? "message" : event, data: data.joined(separator: "\n"))
    }
}
