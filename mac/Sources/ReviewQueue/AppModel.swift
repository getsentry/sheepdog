import Foundation
import Observation
import OSLog

private let log = Logger(subsystem: "com.alwx.reviewqueue", category: "server")

enum AuthState: Equatable {
    case checking
    case signedOut
    case signedIn
}

struct TokenStore: Sendable {
    let read: @Sendable () throws -> String?
    let save: @Sendable (String) throws -> Void
    let delete: @Sendable () -> Void

    private static let account = "session-token"

    static let keychain = TokenStore(
        read: { try Keychain.read(account) },
        save: { try Keychain.save($0, for: account) },
        delete: { Keychain.delete(account) }
    )
}

struct AddressError: LocalizedError {
    let errorDescription: String?
}

@MainActor
@Observable
final class AppModel {
    static let serverURLKey = "serverURL"

    var auth: AuthState = .checking
    var serverURL = UserDefaults.standard.string(forKey: AppModel.serverURLKey) ?? ""
    var signInError: String?
    var settings: API.Settings?
    var settingsError: String?
    var models: [String]?
    var modelsError: String?
    var repositories: [String]?
    var repositoriesError: String?
    private var isLoadingRepositories = false
    // The last poll of the server. nil until the event stream sends it.
    var requests: API.Requests?
    var selection: API.Row.ID?
    // Set while the event stream is down.
    var connectionError: String?
    // The last failed action in the main window.
    var error: String?
    var tasks: [API.RunningTask] = []
    // Keyed by "owner/name#number".
    var reviews: [String: API.Review] = [:]
    var reviewErrors: [String: String] = [:]
    private(set) var startingReviews: Set<String> = []
    var classifications: [String: API.Classification] = [:]
    var classificationErrors: [String: String] = [:]
    private(set) var startingClassifications: Set<String> = []

    @ObservationIgnored private let session: URLSession
    @ObservationIgnored private let tokens: TokenStore
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private var client: APIClient?
    @ObservationIgnored private var stream: Task<Void, Never>?
    // Changes with each session, so that late results of an old session are dropped.
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private var tasksVersion = 0

    init(
        session: URLSession = .shared,
        tokens: TokenStore = .keychain,
        sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }
    ) {
        self.session = session
        self.tokens = tokens
        self.sleep = sleep
    }

    var repo: String { requests?.repo ?? settings?.repo ?? "" }

    // The server sends repository names in lower case, and GitHub ignores their case.
    static func key(repo: String, number: Int) -> String { "\(repo.lowercased())#\(number)" }

    func start() async {
        let token: String?
        do {
            token = try tokens.read()
        } catch {
            signInError = error.localizedDescription
            auth = .signedOut
            return
        }
        guard let token else {
            auth = .signedOut
            return
        }
        do {
            open(makeClient(try Self.serverAddress(serverURL), token: token))
        } catch {
            tokens.delete()
            signInError = error.localizedDescription
            auth = .signedOut
        }
    }

    func signIn(server: String, githubToken: String) async {
        signInError = nil
        do {
            let url = try Self.serverAddress(server)
            let session = try await makeClient(url, token: nil).signIn(githubToken: githubToken.trimmingCharacters(in: .whitespacesAndNewlines))
            try tokens.save(session.sessionToken)
            serverURL = url.absoluteString
            UserDefaults.standard.set(serverURL, forKey: Self.serverURLKey)
            log.info("signed in as \(session.login, privacy: .public)")
            open(makeClient(url, token: session.sessionToken))
        } catch {
            signInError = error.localizedDescription
        }
    }

    // The local session ends first, so that an unreachable server does not delay it.
    func signOut() async {
        guard let client else { return }
        endSession(message: nil)
        do {
            try await client.signOut()
        } catch {
            log.error("sign out failed: \(error.localizedDescription, privacy: .public)")
        }
    }

    func signOutEverywhere() async {
        guard let client else { return }
        settingsError = nil
        // The server ends the streams of the user, and a stopped stream does not connect again with the ended session.
        stream?.cancel()
        do {
            try await checked { try await $0.signOutEverywhere() }
            endSession(message: nil)
        } catch is CancellationError {
        } catch {
            settingsError = error.localizedDescription
            startStream(client)
        }
    }

    func loadSettings() async {
        settings = await call(reporting: \.settingsError) { try await $0.settings() } ?? settings
    }

    func selectRepo(_ name: String) async {
        settings = await call(reporting: \.error) { try await $0.updateSettings(repo: name) } ?? settings
    }

    func saveOpenRouterKey(_ key: String) async -> Bool {
        guard let updated = await call(reporting: \.settingsError, { try await $0.updateSettings(openRouterKey: key) }) else { return false }
        settings = updated
        models = nil
        modelsError = nil
        return true
    }

    func selectModel(_ id: String) async {
        settings = await call(reporting: \.settingsError) { try await $0.updateSettings(model: id) } ?? settings
    }

    func loadModels() async {
        guard models == nil else { return }
        modelsError = nil
        do {
            models = try await checked { try await $0.models() }
        } catch is CancellationError {
        } catch {
            modelsError = error.localizedDescription
        }
    }

    // The window and the dropdown both start a load, and a late failure must not hide a list that loaded.
    func loadRepositories() async {
        guard repositories == nil, !isLoadingRepositories else { return }
        isLoadingRepositories = true
        defer { isLoadingRepositories = false }
        repositoriesError = nil
        do {
            repositories = try await checked { try await $0.repos() }
        } catch is CancellationError {
        } catch {
            repositoriesError = error.localizedDescription
        }
    }

    func pullRequest(repo: String, number: Int) async throws -> API.PullRequest {
        try await checked { try await $0.pullRequest(repo: repo, number: number) }
    }

    // A review event can arrive while the request runs, so an older review never replaces a newer one, and a new error stays.
    // The server does not store failed reviews, so a reload that finds no review keeps the error.
    func loadReview(repo: String, number: Int) async {
        let key = Self.key(repo: repo, number: number)
        let previousError = reviewErrors[key]
        do {
            guard let review = try await checked({ try await $0.review(repo: repo, number: number) }) else { return }
            if review.createdAt >= reviews[key]?.createdAt ?? .distantPast {
                reviews[key] = review
            }
            if reviewErrors[key] == previousError { reviewErrors[key] = nil }
        } catch is CancellationError {
        } catch {
            reviewErrors[key] = error.localizedDescription
        }
    }

    func isReviewing(repo: String, number: Int) -> Bool {
        let key = Self.key(repo: repo, number: number)
        return startingReviews.contains(key) || tasks.contains { $0.kind == "review" && $0.key.lowercased() == key }
    }

    func startReview(repo: String, number: Int) async {
        let key = Self.key(repo: repo, number: number)
        guard !isReviewing(repo: repo, number: number) else { return }
        reviewErrors[key] = nil
        startingReviews.insert(key)
        defer { startingReviews.remove(key) }
        let version = tasksVersion
        do {
            let task = try await checked { try await $0.startReview(repo: repo, number: number) }
            // A tasks event after the start has the current list, which can be without this task if it already ended.
            if version == tasksVersion, !tasks.contains(where: { $0.id == task.id }) { tasks.append(task) }
        } catch is CancellationError {
        } catch {
            reviewErrors[key] = error.localizedDescription
        }
    }

    // Works as loadReview does.
    func loadClassification(repo: String, number: Int) async {
        let key = Self.key(repo: repo, number: number)
        let previousError = classificationErrors[key]
        do {
            guard let classification = try await checked({ try await $0.classification(repo: repo, number: number) }) else { return }
            if classification.createdAt >= classifications[key]?.createdAt ?? .distantPast {
                classifications[key] = classification
            }
            if classificationErrors[key] == previousError { classificationErrors[key] = nil }
        } catch is CancellationError {
        } catch {
            classificationErrors[key] = error.localizedDescription
        }
    }

    func isClassifying(repo: String, number: Int) -> Bool {
        let key = Self.key(repo: repo, number: number)
        return startingClassifications.contains(key) || tasks.contains { $0.kind == "classify" && $0.key.lowercased() == key }
    }

    func startClassification(repo: String, number: Int) async {
        let key = Self.key(repo: repo, number: number)
        guard !isClassifying(repo: repo, number: number) else { return }
        classificationErrors[key] = nil
        startingClassifications.insert(key)
        defer { startingClassifications.remove(key) }
        let version = tasksVersion
        do {
            let task = try await checked { try await $0.startClassification(repo: repo, number: number) }
            if version == tasksVersion, !tasks.contains(where: { $0.id == task.id }) { tasks.append(task) }
        } catch is CancellationError {
        } catch {
            classificationErrors[key] = error.localizedDescription
        }
    }

    func cancelTask(_ id: String) async {
        error = nil
        do {
            try await checked { try await $0.cancelTask(id: id) }
        } catch APIError.server(status: 404, _) {
        } catch is CancellationError {
            return
        } catch {
            self.error = error.localizedDescription
            return
        }
        tasks.removeAll { $0.id == id }
    }

    func apply(_ event: API.Event) {
        switch event {
        case .requests(let value):
            requests = value
        case .tasks(let value):
            tasksVersion += 1
            tasks = value
        case .review(let outcome):
            let key = Self.key(repo: outcome.repo, number: outcome.number)
            if let review = outcome.review {
                reviews[key] = review
                reviewErrors[key] = nil
            } else if let message = outcome.error {
                reviewErrors[key] = message
            }
        case .classification(let outcome):
            let key = Self.key(repo: outcome.repo, number: outcome.number)
            if let classification = outcome.classification {
                classifications[key] = classification
                classificationErrors[key] = nil
            } else if let message = outcome.error {
                classificationErrors[key] = message
            }
        }
    }

    private func handle(_ update: StreamUpdate) {
        switch update {
        case .connected:
            connectionError = nil
            if repositoriesError != nil {
                Task { await loadRepositories() }
            }
            // The server does not send again the review and classification events that the client missed while the stream was down.
            if let number = selection, !repo.isEmpty {
                let repo = repo
                Task { await loadReview(repo: repo, number: number) }
                Task { await loadClassification(repo: repo, number: number) }
            }
        case .disconnected(let message):
            connectionError = "No connection to the server. \(message)"
        case .event(let event):
            apply(event)
        }
    }

    private func makeClient(_ url: URL, token: String?) -> APIClient {
        APIClient(baseURL: url, sessionToken: token, session: session, sleep: sleep)
    }

    private func open(_ client: APIClient) {
        generation += 1
        self.client = client
        auth = .signedIn
        Task { await loadSettings() }
        startStream(client)
    }

    private func startStream(_ client: APIClient) {
        let generation = generation
        stream = Task { [weak self] in
            do {
                for try await update in client.events() {
                    guard let self, self.generation == generation else { return }
                    self.handle(update)
                }
            } catch {
                guard let self, self.generation == generation else { return }
                self.endSession(message: error.localizedDescription)
            }
        }
    }

    private func endSession(message: String?) {
        generation += 1
        stream?.cancel()
        stream = nil
        client = nil
        tokens.delete()
        settings = nil
        settingsError = nil
        models = nil
        modelsError = nil
        repositories = nil
        repositoriesError = nil
        requests = nil
        selection = nil
        connectionError = nil
        error = nil
        tasks = []
        reviews = [:]
        reviewErrors = [:]
        startingReviews = []
        classifications = [:]
        classificationErrors = [:]
        startingClassifications = []
        signInError = message
        auth = .signedOut
    }

    // Throws CancellationError for a result that no longer matters: a cancelled request, a result of an ended session, or a 401 or 403 that ended the session.
    private func checked<T>(_ body: (APIClient) async throws -> T) async throws -> T {
        guard let client else { throw CancellationError() }
        let generation = generation
        do {
            let value = try await body(client)
            guard generation == self.generation else { throw CancellationError() }
            return value
        } catch let error as APIError where error.endsSession {
            if generation == self.generation { endSession(message: error.localizedDescription) }
            throw CancellationError()
        } catch {
            if generation != self.generation || Task.isCancelled || (error as? URLError)?.code == .cancelled {
                throw CancellationError()
            }
            throw error
        }
    }

    // Clears the error field, and sets it on a failure.
    private func call<T>(reporting field: ReferenceWritableKeyPath<AppModel, String?>, _ body: (APIClient) async throws -> T) async -> T? {
        self[keyPath: field] = nil
        do {
            return try await checked(body)
        } catch is CancellationError {
            return nil
        } catch {
            self[keyPath: field] = error.localizedDescription
            return nil
        }
    }

    // App Transport Security blocks plain HTTP to host names.
    static func serverAddress(_ text: String) throws -> URL {
        let text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: text), let scheme = url.scheme?.lowercased(), ["http", "https"].contains(scheme),
              let host = url.host(), !host.isEmpty else {
            throw AddressError(errorDescription: "Enter the server address with https://, for example https://reviews.example.com. For a server on this Mac, use http://127.0.0.1:8080.")
        }
        guard scheme == "https" || allowsPlainHTTP(host) else {
            throw AddressError(errorDescription: "Use https:// for \(host). Plain http:// works only for localhost and IP addresses.")
        }
        return url
    }

    private static func allowsPlainHTTP(_ host: String) -> Bool {
        var v4 = in_addr()
        var v6 = in6_addr()
        return host.lowercased() == "localhost" || inet_pton(AF_INET, host, &v4) == 1 || inet_pton(AF_INET6, host, &v6) == 1
    }
}
