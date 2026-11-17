import Foundation
import Synchronization
import Testing
@testable import Sheepdog

private let start = Date(timeIntervalSince1970: 1_800_000_000)

private func task(_ key: String) -> API.RunningTask {
    API.RunningTask(id: "T1", kind: "review", key: key, title: "Review of #6773", subtitle: "getsentry/sentry-react-native", startedAt: start)
}

private let review = API.Review(
    repo: "getsentry/sentry-react-native", number: 6773, headSha: "03da17f", model: "google/gemini-4-hydrogen",
    risk: .yellow, summary: "Updates the JavaScript SDK.", findings: [], diffTruncated: false, prUpdatedAt: start, createdAt: start
)

@MainActor
@Test func eventsUpdateTheModel() {
    let model = AppModel(tokens: MemoryTokens(nil).store)
    let repo = "getsentry/sentry-react-native"
    model.apply(.requests(API.Requests(repo: repo, fetchedAt: start, rows: [], lanes: [], rulesError: nil, error: nil)))
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
@Test func reviewKeysIgnoreCase() {
    let model = AppModel(tokens: MemoryTokens(nil).store)
    model.apply(.tasks([task("getsentry/sentry-react-native#6773")]))
    #expect(model.isReviewing(repo: "GetSentry/Sentry-React-Native", number: 6773))
    model.apply(.review(API.ReviewOutcome(repo: "getsentry/sentry-react-native", number: 6773, review: review, error: nil)))
    #expect(model.reviews[AppModel.key(repo: "getsentry/Sentry-React-Native", number: 6773)] == review)
}

// C5
@MainActor
@Test func plainHTTPOnlyForLocalhostAndIPAddresses() throws {
    for address in [" http://127.0.0.1:8080 ", "http://localhost:8080", "http://LocalHost", "http://[::1]:8080", "http://100.64.1.2:8080", "https://box.tailnet.ts.net", "HTTPS://reviews.example.com/"] {
        #expect((try? AppModel.serverAddress(address)) != nil, "\(address)")
    }
    #expect(try AppModel.serverAddress("https://reviews.example.com/").host() == "reviews.example.com")

    let hostName = #expect(throws: AddressError.self) { try AppModel.serverAddress("http://box.tailnet.ts.net:8080") }
    #expect(hostName?.errorDescription == "Use https:// for box.tailnet.ts.net. Plain http:// works only for localhost and IP addresses.")
    #expect(throws: AddressError.self) { try AppModel.serverAddress("http://127.1") }
    for address in ["127.0.0.1:8080", "ftp://example.com", ""] {
        let error = #expect(throws: AddressError.self) { try AppModel.serverAddress(address) }
        #expect(error?.errorDescription?.hasPrefix("Enter the server address with https://") == true)
    }
}

private let settingsJSON = #"{"repo":"acme/app","model":"a/model","hasOpenRouterKey":true}"#
// The tasks event comes first here, so that a loaded requests event means that the first events are applied.
private let streamBody = """
event: tasks
data: []

event: requests
data: {"repo":"acme/app","fetchedAt":null,"rows":[],"lanes":[]}


"""
private let taskJSON = #"{"id":"T1","kind":"review","key":"acme/app#1","title":"Review of #1","subtitle":"acme/app","startedAt":"2026-11-16T10:00:00Z"}"#
private let notAllowed = "This GitHub account is not allowed on this server"

private func reviewJSON(_ summary: String, createdAt: String) -> String {
    #"{"repo":"acme/app","number":1,"headSha":"abc","model":"a/model","risk":"green","summary":"\#(summary)","findings":[],"diffTruncated":false,"prUpdatedAt":"2026-11-16T08:00:00Z","createdAt":"\#(createdAt)"}"#
}

private func error(_ status: Int, _ message: String) -> Reply {
    Reply(status: status, body: #"{"error":"\#(message)"}"#)
}

private let neverReconnect: @Sendable (Duration) async throws -> Void = { _ in try await Task.sleep(for: .seconds(3600)) }

// The first reconnect waits for the gate. Later reconnects wait until the stream ends.
private func reconnectOnce(after gate: Gate) -> @Sendable (Duration) async throws -> Void {
    let calls = Mutex(0)
    return { _ in
        if calls.withLock({ $0 += 1; return $0 }) == 1 {
            await gate.wait()
        } else {
            try await Task.sleep(for: .seconds(3600))
        }
    }
}

// Starts a model with a stored session. A nil reply from the handler gives the usual reply of the server.
@MainActor
private func signedIn(
    token: String = "A",
    sleep: @escaping @Sendable (Duration) async throws -> Void = neverReconnect,
    handler: @escaping @Sendable (Seen) async -> Reply?
) async -> (AppModel, MemoryTokens) {
    let (url, session) = MockProtocol.server { request in
        if let reply = await handler(request) { return reply }
        switch (request.method, request.path) {
        case ("GET", "/api/events"): return Reply(status: 200, body: streamBody)
        case (_, "/api/settings"): return Reply(status: 200, body: settingsJSON)
        case ("GET", "/api/repos/acme/app/classifications"): return Reply(status: 200, body: "[]")
        case ("DELETE", _): return Reply(status: 204, body: "")
        default: return error(404, "Not found")
        }
    }
    let tokens = MemoryTokens(token)
    let model = AppModel(session: session, tokens: tokens.store, sleep: sleep)
    model.serverURL = url.absoluteString
    await model.start()
    return (model, tokens)
}

// C1
@MainActor
@Test func cancelledReviewLoadLeavesNoError() async {
    let gate = Gate()
    let calls = Mutex(0)
    let (model, _) = await signedIn { request in
        guard request.path == "/api/repos/acme/app/pulls/1/review" else { return nil }
        switch calls.withLock({ $0 += 1; return $0 }) {
        case 1:
            await gate.wait()
            return Reply(status: 200, body: reviewJSON("Late", createdAt: "2026-11-16T09:00:00Z"))
        case 2: return error(500, "Internal server error")
        default: return Reply(status: 200, body: reviewJSON("Fine", createdAt: "2026-11-16T09:00:00Z"))
        }
    }
    let load = Task { await model.loadReview(repo: "acme/app", number: 1) }
    #expect(await until { calls.withLock { $0 } == 1 })
    load.cancel()
    await load.value
    #expect(model.reviewErrors.isEmpty)
    #expect(model.reviews.isEmpty)
    await gate.open()

    await model.loadReview(repo: "acme/app", number: 1)
    #expect(model.reviewErrors["acme/app#1"] == "Internal server error")
    await model.loadReview(repo: "acme/app", number: 1)
    #expect(model.reviewErrors.isEmpty)
    #expect(model.reviews["acme/app#1"]?.summary == "Fine")
    await model.signOut()
}

// C2
@MainActor
@Test func reconnectReloadsTheReviewOfTheOpenPR() async {
    let reconnect = Gate()
    let stored = Mutex(reviewJSON("Old", createdAt: "2026-11-16T09:00:00Z"))
    let connections = Mutex(0)
    let (model, _) = await signedIn(sleep: reconnectOnce(after: reconnect)) { request in
        switch request.path {
        case "/api/events":
            connections.withLock { $0 += 1 }
            return nil
        case "/api/repos/acme/app/pulls/1/review": return Reply(status: 200, body: stored.withLock { $0 })
        default: return nil
        }
    }
    #expect(await until { model.requests != nil })
    model.selection = 1
    await model.loadReview(repo: "acme/app", number: 1)
    #expect(model.reviews["acme/app#1"]?.summary == "Old")

    // The review finishes on the server while the stream is down.
    stored.withLock { $0 = reviewJSON("New", createdAt: "2026-11-16T10:00:00Z") }
    await model.loadReview(repo: "acme/app", number: 1)
    #expect(model.reviews["acme/app#1"]?.summary == "New")
    stored.withLock { $0 = reviewJSON("Newer", createdAt: "2026-11-16T11:00:00Z") }
    await reconnect.open()
    #expect(await until { model.reviews["acme/app#1"]?.summary == "Newer" })
    #expect(connections.withLock { $0 } == 2)
    await model.signOut()
}

// C3
@MainActor
@Test func lostConnectionIsShownUntilTheStreamIsBack() async {
    let reconnect = Gate()
    let connections = Mutex(0)
    let (model, _) = await signedIn(sleep: reconnectOnce(after: reconnect)) { request in
        guard request.path == "/api/events", connections.withLock({ $0 += 1; return $0 }) == 1 else { return nil }
        return Reply(status: 502, body: "")
    }
    #expect(await until { model.connectionError != nil })
    #expect(model.connectionError == "No connection to the server. The server returned HTTP 502")
    #expect(model.error == nil)
    await reconnect.open()
    #expect(await until { model.requests != nil })
    #expect(model.connectionError == nil)
    await model.signOut()
}

// C4 and C11
@MainActor
@Test func signOutForgetsTheModelsOfTheUser() async {
    let (model, tokens) = await signedIn { request in
        switch (request.method, request.path) {
        case ("GET", "/api/models"): Reply(status: 200, body: #"["a/model"]"#)
        case ("PUT", "/api/settings"): error(500, "Disk full")
        default: nil
        }
    }
    await model.loadModels()
    #expect(model.models == ["a/model"])
    await model.selectModel("a/model")
    #expect(model.settingsError == "Disk full")
    #expect(model.error == nil)

    await model.signOut()
    #expect(model.auth == .signedOut)
    #expect(model.models == nil && model.modelsError == nil && model.settingsError == nil)
    #expect(tokens.value == nil)
}

// C7
@MainActor
@Test func signOutEndsTheLocalSessionBeforeTheServerAnswers() async {
    let gate = Gate()
    let deletes = Mutex(0)
    let (model, tokens) = await signedIn { request in
        guard request.method == "DELETE" else { return nil }
        deletes.withLock { $0 += 1 }
        await gate.wait()
        return Reply(status: 204, body: "")
    }
    let signOut = Task { await model.signOut() }
    #expect(await until { deletes.withLock { $0 } == 1 })
    #expect(model.auth == .signedOut)
    #expect(tokens.value == nil)
    await gate.open()
    await signOut.value
    #expect(model.signInError == nil)
}

@MainActor
@Test func signOutOnAllDevices() async {
    let fails = Mutex(true)
    let seen = Mutex<[String]>([])
    let (model, tokens) = await signedIn { request in
        seen.withLock { $0.append("\(request.authorization ?? "") \(request.method) \(request.path)") }
        guard request.path == "/api/sessions" else { return nil }
        return fails.withLock { $0 } ? error(500, "Internal server error") : Reply(status: 204, body: "")
    }
    #expect(await until { model.requests != nil })
    await model.signOutEverywhere()
    #expect(model.auth == .signedIn)
    #expect(model.settingsError == "Internal server error")
    #expect(tokens.value == "A")
    #expect(await until { seen.withLock { $0.filter { $0.hasSuffix("/api/events") }.count } == 2 })
    #expect(model.settingsError == "Internal server error")

    fails.withLock { $0 = false }
    await model.signOutEverywhere()
    #expect(model.auth == .signedOut)
    #expect(model.signInError == nil)
    #expect(tokens.value == nil)
    #expect(seen.withLock { $0.filter { $0.hasSuffix("/api/sessions") } } == ["Bearer A DELETE /api/sessions", "Bearer A DELETE /api/sessions"])
}

@MainActor
@Test func refusedSavedAddressDeletesTheSessionToken() async {
    let tokens = MemoryTokens("A")
    let model = AppModel(tokens: tokens.store)
    model.serverURL = "http://box.tailnet.ts.net:8080"
    await model.start()
    #expect(model.auth == .signedOut)
    #expect(model.signInError == "Use https:// for box.tailnet.ts.net. Plain http:// works only for localhost and IP addresses.")
    #expect(tokens.value == nil)
}

@MainActor
@Test func repositoriesLoadAgainAfterAFailure() async {
    let reconnect = Gate()
    let loads = Mutex(0)
    let (model, _) = await signedIn(sleep: reconnectOnce(after: reconnect)) { request in
        guard request.path == "/api/repos" else { return nil }
        return switch loads.withLock({ $0 += 1; return $0 }) {
        case 1, 2: error(502, "GitHub is not available")
        default: Reply(status: 200, body: #"["acme/app"]"#)
        }
    }
    #expect(await until { model.requests != nil })
    await model.loadRepositories()
    #expect(model.repositoriesError == "GitHub is not available")
    // The dropdown opens again.
    await model.loadRepositories()
    #expect(loads.withLock { $0 } == 2)
    #expect(model.repositories == nil)

    await reconnect.open()
    #expect(await until { model.repositories == ["acme/app"] })
    #expect(model.repositoriesError == nil)
    await model.signOut()
}

@MainActor
@Test func concurrentRepositoryLoadsSendOneRequest() async {
    let gate = Gate()
    let loads = Mutex(0)
    let (model, _) = await signedIn { request in
        guard request.path == "/api/repos" else { return nil }
        loads.withLock { $0 += 1 }
        await gate.wait()
        return Reply(status: 200, body: #"["acme/app"]"#)
    }
    #expect(await until { model.requests != nil })
    async let first: Void = model.loadRepositories()
    #expect(await until { loads.withLock { $0 } == 1 })
    await model.loadRepositories()
    await gate.open()
    await first
    #expect(loads.withLock { $0 } == 1)
    #expect(model.repositories == ["acme/app"])
    #expect(model.repositoriesError == nil)
    await model.signOut()
}

@MainActor
@Test func reviewReloadKeepsReviewErrors() async {
    let gate = Gate()
    let calls = Mutex(0)
    let (model, _) = await signedIn { request in
        guard request.path == "/api/repos/acme/app/pulls/1/review" else { return nil }
        guard calls.withLock({ $0 += 1; return $0 }) > 1 else { return error(404, "No review") }
        await gate.wait()
        return Reply(status: 200, body: reviewJSON("Old", createdAt: "2026-11-16T09:00:00Z"))
    }
    model.apply(.review(API.ReviewOutcome(repo: "acme/app", number: 1, review: nil, error: "Invalid API key")))
    await model.loadReview(repo: "acme/app", number: 1)
    #expect(model.reviewErrors["acme/app#1"] == "Invalid API key")

    let load = Task { await model.loadReview(repo: "acme/app", number: 1) }
    #expect(await until { calls.withLock { $0 } == 2 })
    model.apply(.review(API.ReviewOutcome(repo: "acme/app", number: 1, review: nil, error: "GitHub: Not Found")))
    await gate.open()
    await load.value
    #expect(model.reviews["acme/app#1"]?.summary == "Old")
    #expect(model.reviewErrors["acme/app#1"] == "GitHub: Not Found")

    await model.loadReview(repo: "acme/app", number: 1)
    #expect(model.reviewErrors.isEmpty)
    await model.signOut()
}

// C8
@MainActor
@Test func lateResultsOfAnOldSessionAreDropped() async {
    let gate = Gate()
    let held = Mutex(0)
    let (model, tokens) = await signedIn { request in
        switch (request.authorization, request.method, request.path) {
        case ("Bearer A", "PUT", "/api/settings"):
            held.withLock { $0 += 1 }
            await gate.wait()
            return Reply(status: 200, body: #"{"repo":"acme/old","model":"","hasOpenRouterKey":false}"#)
        case ("Bearer A", "GET", "/api/repos"):
            held.withLock { $0 += 1 }
            await gate.wait()
            return error(401, "The session is not valid. Sign in again")
        case ("Bearer B", "GET", "/api/settings"):
            return Reply(status: 200, body: #"{"repo":"bob/new","model":"","hasOpenRouterKey":false}"#)
        default: return nil
        }
    }
    let select = Task { await model.selectRepo("acme/old") }
    let repos = Task { await model.loadRepositories() }
    #expect(await until { held.withLock { $0 } == 2 })

    await model.signOut()
    tokens.value = "B"
    await model.start()
    #expect(await until { model.settings?.repo == "bob/new" })

    await gate.open()
    await select.value
    await repos.value
    #expect(model.settings?.repo == "bob/new")
    #expect(model.auth == .signedIn)
    #expect(tokens.value == "B")
    #expect(model.repositories == nil && model.repositoriesError == nil)
    #expect(model.error == nil && model.signInError == nil)
    await model.signOut()
}

// C9
@MainActor
@Test func fastFailedReviewDoesNotStayRunning() async {
    let gate = Gate()
    let posts = Mutex(0)
    let (model, _) = await signedIn { request in
        guard request.method == "POST" else { return nil }
        posts.withLock { $0 += 1 }
        await gate.wait()
        return Reply(status: 202, body: taskJSON)
    }
    #expect(await until { model.requests != nil })
    let start = Task { await model.startReview(repo: "acme/app", number: 1) }
    #expect(await until { posts.withLock { $0 } == 1 })
    #expect(model.isReviewing(repo: "acme/app", number: 1))

    // The events of the start and of the failure arrive before the POST response.
    let running = try? API.decode(API.RunningTask.self, from: Data(taskJSON.utf8))
    model.apply(.tasks(running.map { [$0] } ?? []))
    model.apply(.review(API.ReviewOutcome(repo: "acme/app", number: 1, review: nil, error: "GitHub: Not Found")))
    model.apply(.tasks([]))
    await gate.open()
    await start.value
    #expect(!model.isReviewing(repo: "acme/app", number: 1))
    #expect(model.tasks.isEmpty)
    #expect(model.reviewErrors["acme/app#1"] == "GitHub: Not Found")
    await model.signOut()
}

@MainActor
@Test func classificationEventsAreSeparateFromReviews() {
    let model = AppModel(tokens: MemoryTokens(nil).store)
    let repo = "getsentry/sentry-react-native"
    let classifying = API.RunningTask(id: "T2", kind: "classify", key: "\(repo)#6773", title: "Classification of #6773", subtitle: repo, startedAt: start)
    model.apply(.tasks([classifying]))
    #expect(model.isClassifying(repo: repo, number: 6773))
    #expect(!model.isReviewing(repo: repo, number: 6773))

    model.apply(.classification(API.ClassificationOutcome(repo: repo, number: 6773, classification: nil, error: "Invalid API key")))
    #expect(model.classificationErrors["\(repo)#6773"] == "Invalid API key")
    #expect(model.reviewErrors.isEmpty)

    let classification = API.Classification(
        repo: repo, number: 6773, headSha: "03da17f", model: "google/gemini-4-hydrogen",
        score: 3, reason: "Only a dependency bump.", diffTruncated: false, prUpdatedAt: start, createdAt: start
    )
    model.apply(.classification(API.ClassificationOutcome(repo: repo, number: 6773, classification: classification, error: nil)))
    model.apply(.tasks([]))
    #expect(model.classifications["\(repo)#6773"] == classification)
    #expect(model.classificationErrors.isEmpty)
    #expect(model.reviews.isEmpty)
    #expect(!model.isClassifying(repo: repo, number: 6773))
}

private func classificationJSON(number: Int, score: Int, createdAt: String) -> String {
    #"{"repo":"acme/app","number":\#(number),"headSha":"abc","model":"m","score":\#(score),"reason":"r","diffTruncated":false,"prUpdatedAt":"2026-11-16T10:00:00Z","createdAt":"\#(createdAt)"}"#
}

@MainActor
@Test func listClassificationsLoadForTheRepositoryAndAfterReconnect() async {
    let reconnect = Gate()
    let loads = Mutex(0)
    let (model, _) = await signedIn(sleep: reconnectOnce(after: reconnect)) { request in
        guard request.path == "/api/repos/acme/app/classifications" else { return nil }
        let score = loads.withLock { $0 += 1; return $0 } == 1 ? 2 : 9
        return Reply(status: 200, body: "[\(classificationJSON(number: 1, score: score, createdAt: score == 2 ? "2026-11-16T10:00:00Z" : "2026-11-16T11:00:00Z")),\(classificationJSON(number: 4, score: 6, createdAt: "2026-11-16T10:00:00Z"))]")
    }
    #expect(await until { model.classifications["acme/app#4"]?.score == 6 })
    #expect(model.classifications["acme/app#1"]?.score == 2)
    #expect(loads.withLock { $0 } == 1)

    await reconnect.open()
    #expect(await until { model.classifications["acme/app#1"]?.score == 9 })
    #expect(loads.withLock { $0 } == 2)
    #expect(model.error == nil)
    await model.signOut()
    #expect(model.classifications.isEmpty)
}

@MainActor
@Test func classificationLoadsAndStarts() async {
    let (model, _) = await signedIn { request in
        switch (request.method, request.path) {
        case ("GET", "/api/repos/acme/app/pulls/1/classification"):
            return Reply(status: 200, body: #"{"repo":"acme/app","number":1,"headSha":"abc","model":"m","score":8,"reason":"Touches auth.","diffTruncated":true,"prUpdatedAt":"2026-11-16T10:00:00Z","createdAt":"2026-11-16T10:30:12.345Z"}"#)
        case ("POST", "/api/repos/acme/app/pulls/2/classification"):
            return error(409, "A classification of this pull request is already running")
        default:
            return nil
        }
    }
    #expect(await until { model.requests != nil })
    await model.loadClassification(repo: "acme/app", number: 1)
    #expect(model.classifications["acme/app#1"]?.score == 8)
    #expect(model.classifications["acme/app#1"]?.diffTruncated == true)
    await model.startClassification(repo: "acme/app", number: 2)
    #expect(model.classificationErrors["acme/app#2"] == "A classification of this pull request is already running")
    #expect(model.reviewErrors.isEmpty)
    await model.signOut()
}

// C10
@MainActor
@Test func doubleClickStartsOneReview() async {
    let gate = Gate()
    let posts = Mutex(0)
    let (model, _) = await signedIn { request in
        guard request.method == "POST" else { return nil }
        guard posts.withLock({ $0 += 1; return $0 }) == 1 else { return error(409, "A review of this pull request is already running") }
        await gate.wait()
        return Reply(status: 202, body: taskJSON)
    }
    #expect(await until { model.requests != nil })
    let first = Task { await model.startReview(repo: "acme/app", number: 1) }
    #expect(await until { posts.withLock { $0 } == 1 })
    await model.startReview(repo: "acme/app", number: 1)
    await gate.open()
    await first.value
    #expect(posts.withLock { $0 } == 1)
    #expect(model.reviewErrors.isEmpty)
    // No tasks event came after the start, so the task from the response shows.
    #expect(model.tasks.map(\.id) == ["T1"])
    #expect(model.isReviewing(repo: "acme/app", number: 1))
    await model.signOut()
}

// A login that is removed from the allowlist gets 403 on the stream and on every route.
@MainActor
@Test func notAllowedAccountEndsTheSession() async {
    let (streamModel, streamTokens) = await signedIn { request in
        request.path == "/api/events" ? error(403, notAllowed) : nil
    }
    #expect(await until { streamModel.auth == .signedOut })
    #expect(streamModel.signInError == notAllowed)
    #expect(streamTokens.value == nil)

    let gate = Gate()
    let (routeModel, routeTokens) = await signedIn { request in
        switch request.path {
        case "/api/events":
            await gate.wait()
            return nil
        case "/api/repos": return error(403, notAllowed)
        default: return nil
        }
    }
    await routeModel.loadRepositories()
    #expect(routeModel.auth == .signedOut)
    #expect(routeModel.signInError == notAllowed)
    #expect(routeModel.repositoriesError == nil)
    #expect(routeTokens.value == nil)
    await gate.open()
}

@MainActor
@Test func approveAndMergeRunsOneAfterAnotherAndGoesOnAfterAFailure() async throws {
    let gate = Gate()
    let seen = Mutex<[String]>([])
    let running = Mutex((now: 0, most: 0))
    let (model, _) = await signedIn { request in
        guard request.path.hasSuffix("/approve-and-merge") else { return nil }
        let body = (try? JSONDecoder().decode([String: String].self, from: request.body ?? Data())) ?? [:]
        seen.withLock { $0.append("\(request.path) \(body["headSha"] ?? "")") }
        running.withLock { $0.now += 1; $0.most = max($0.most, $0.now) }
        defer { running.withLock { $0.now -= 1 } }
        if request.path.contains("/pulls/1/") { await gate.wait() }
        return request.path.contains("/pulls/2/")
            ? error(502, "GitHub: Can not approve your own pull request")
            : Reply(status: 200, body: #"{"result":"merged"}"#)
    }
    #expect(await until { model.requests != nil })
    let rows = [1, 2, 3].map { laneRow($0, lane: "safe", action: API.Lane.approveAndMerge) }
    let batch = Task { await model.approveAndMerge(rows) }
    #expect(await until { seen.withLock { $0.count } == 1 })
    #expect(model.isMerging(repo: "acme/app", number: 1))
    #expect(!model.isMerging(repo: "acme/app", number: 2))
    // A second click on the running PR sends nothing.
    await model.approveAndMerge([rows[0]])
    await gate.open()
    await batch.value

    #expect(seen.withLock { $0 } == [
        "/api/repos/acme/app/pulls/1/approve-and-merge sha1",
        "/api/repos/acme/app/pulls/2/approve-and-merge sha2",
        "/api/repos/acme/app/pulls/3/approve-and-merge sha3",
    ])
    #expect(running.withLock { $0.most } == 1)
    #expect(model.error == "Approve and merge failed.\n#2: GitHub: Can not approve your own pull request")
    #expect(model.merging.isEmpty)
    await model.signOut()
}
