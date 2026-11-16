import Foundation
import Observation
import OSLog

private let log = Logger(subsystem: "com.alwx.reviewqueue", category: "server")

enum AuthState: Equatable {
    case checking
    case signedOut
    case signedIn
}

@MainActor
@Observable
final class AppModel {
    static let serverURLKey = "serverURL"
    static let sessionAccount = "session-token"

    var auth: AuthState = .checking
    var serverURL = UserDefaults.standard.string(forKey: AppModel.serverURLKey) ?? ""
    var signInError: String?
    var settings: API.Settings?
    var repositories: [String]?
    var repositoriesError: String?
    // The last poll of the server. nil until the event stream sends it.
    var requests: API.Requests?
    var error: String?
    var tasks: [API.RunningTask] = []
    // Keyed by "owner/name#number".
    var reviews: [String: API.Review] = [:]
    var reviewErrors: [String: String] = [:]

    private var client: APIClient?
    private var stream: Task<Void, Never>?

    var repo: String { requests?.repo ?? settings?.repo ?? "" }

    static func key(repo: String, number: Int) -> String { "\(repo)#\(number)" }

    func start() async {
        let token: String?
        do {
            token = try Keychain.read(Self.sessionAccount)
        } catch {
            signInError = error.localizedDescription
            auth = .signedOut
            return
        }
        guard let token, let url = Self.serverAddress(serverURL) else {
            auth = .signedOut
            return
        }
        open(APIClient(baseURL: url, sessionToken: token))
    }

    func signIn(server: String, githubToken: String) async {
        signInError = nil
        guard let url = Self.serverAddress(server) else {
            signInError = "Enter the server address with http:// or https://, for example http://127.0.0.1:8080."
            return
        }
        do {
            let session = try await APIClient(baseURL: url).signIn(githubToken: githubToken.trimmingCharacters(in: .whitespacesAndNewlines))
            try Keychain.save(session.sessionToken, for: Self.sessionAccount)
            serverURL = url.absoluteString
            UserDefaults.standard.set(serverURL, forKey: Self.serverURLKey)
            log.info("signed in as \(session.login, privacy: .public)")
            open(APIClient(baseURL: url, sessionToken: session.sessionToken))
        } catch {
            signInError = error.localizedDescription
        }
    }

    // The local session ends also when the server cannot be reached.
    func signOut() async {
        do {
            try await client?.signOut()
        } catch {
            log.error("sign out failed: \(error.localizedDescription, privacy: .public)")
        }
        endSession(message: nil)
    }

    func loadSettings() async {
        settings = await call { try await $0.settings() } ?? settings
    }

    func selectRepo(_ name: String) async {
        settings = await call { try await $0.updateSettings(repo: name) } ?? settings
    }

    func saveOpenRouterKey(_ key: String) async -> Bool {
        guard let updated = await call({ try await $0.updateSettings(openRouterKey: key) }) else { return false }
        settings = updated
        return true
    }

    func selectModel(_ id: String) async {
        settings = await call { try await $0.updateSettings(model: id) } ?? settings
    }

    func models() async throws -> [String] {
        guard let client else { return [] }
        return try await checked { try await client.models() }
    }

    func loadRepositories() async {
        guard repositories == nil else { return }
        repositoriesError = nil
        guard let client else { return }
        do {
            repositories = try await checked { try await client.repos() }
        } catch {
            repositoriesError = error.localizedDescription
        }
    }

    func pullRequest(repo: String, number: Int) async throws -> API.PullRequest {
        guard let client else { throw CancellationError() }
        return try await checked { try await client.pullRequest(repo: repo, number: number) }
    }

    func loadReview(repo: String, number: Int) async {
        let key = Self.key(repo: repo, number: number)
        guard reviews[key] == nil, let client else { return }
        do {
            if let review = try await checked({ try await client.review(repo: repo, number: number) }) {
                reviews[key] = review
            }
        } catch {
            reviewErrors[key] = error.localizedDescription
        }
    }

    func isReviewing(repo: String, number: Int) -> Bool {
        let key = Self.key(repo: repo, number: number)
        return tasks.contains { $0.kind == "review" && $0.key == key }
    }

    func startReview(repo: String, number: Int) async {
        let key = Self.key(repo: repo, number: number)
        reviewErrors[key] = nil
        guard let client else { return }
        do {
            let task = try await checked { try await client.startReview(repo: repo, number: number) }
            if !tasks.contains(where: { $0.id == task.id }) { tasks.append(task) }
        } catch {
            reviewErrors[key] = error.localizedDescription
        }
    }

    func cancelTask(_ id: String) async {
        guard let client else { return }
        do {
            try await checked { try await client.cancelTask(id: id) }
        } catch APIError.server(status: 404, _) {
        } catch {
            report(error)
            return
        }
        tasks.removeAll { $0.id == id }
    }

    func apply(_ event: API.Event) {
        switch event {
        case .requests(let value):
            requests = value
        case .tasks(let value):
            tasks = value
        case .review(let outcome):
            let key = Self.key(repo: outcome.repo, number: outcome.number)
            if let review = outcome.review {
                reviews[key] = review
                reviewErrors[key] = nil
            } else if let message = outcome.error {
                reviewErrors[key] = message
            }
        }
    }

    private func open(_ client: APIClient) {
        self.client = client
        auth = .signedIn
        stream = Task { [weak self] in
            await self?.loadSettings()
            do {
                for try await event in client.events() {
                    self?.error = nil
                    self?.apply(event)
                }
            } catch APIError.unauthorized(let message) {
                self?.endSession(message: message)
            } catch {
                self?.error = error.localizedDescription
            }
        }
    }

    private func endSession(message: String?) {
        stream?.cancel()
        stream = nil
        client = nil
        Keychain.delete(Self.sessionAccount)
        settings = nil
        repositories = nil
        repositoriesError = nil
        requests = nil
        error = nil
        tasks = []
        reviews = [:]
        reviewErrors = [:]
        signInError = message
        auth = .signedOut
    }

    // A 401 from any route means that the session or the GitHub token is no longer valid.
    private func checked<T>(_ body: () async throws -> T) async throws -> T {
        do {
            return try await body()
        } catch APIError.unauthorized(let message) {
            endSession(message: message)
            throw APIError.unauthorized(message)
        }
    }

    // Shows a failure in the error banner and returns nil.
    @discardableResult
    private func call<T>(_ body: (APIClient) async throws -> T) async -> T? {
        guard let client else { return nil }
        do {
            return try await checked { try await body(client) }
        } catch {
            report(error)
            return nil
        }
    }

    private func report(_ error: any Error) {
        if case APIError.unauthorized = error { return }
        self.error = error.localizedDescription
    }

    static func serverAddress(_ text: String) -> URL? {
        let text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: text), ["http", "https"].contains(url.scheme), url.host() != nil else { return nil }
        return url
    }
}
