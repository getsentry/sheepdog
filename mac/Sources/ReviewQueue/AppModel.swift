import Foundation
import Observation
import OSLog

private let log = Logger(subsystem: "com.alwx.reviewqueue", category: "poll")

enum AuthState: Equatable {
    case checking
    case ghMissing
    case signedOut
    case pending(code: String)
    case signedIn(login: String)
}

@MainActor
@Observable
final class AppModel {
    var auth: AuthState = .checking
    var repositories: [String] = []
    var rows: [PRRow] = []
    var lastRefresh: Date?
    var error: String?
    // Keyed by "owner/name#number". Reviews stay in memory only.
    var reviews: [String: ReviewState] = [:]

    private var loginProcess: Process?

    func start() async {
        guard GH.executable != nil else {
            auth = .ghMissing
            return
        }
        guard await GH.authStatus() else {
            auth = .signedOut
            return
        }
        await loadLogin()
    }

    func signIn() async {
        guard loginProcess == nil, let executable = GH.executable else { return }
        error = nil

        let process = Process()
        process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = ["auth", "login", "--web", "--hostname", "github.com", "--git-protocol", "https", "--skip-ssh-key"]
        process.standardInput = FileHandle.nullDevice
        let pipe = Pipe()
        process.standardOutput = pipe
        process.standardError = pipe
        let (exits, exit) = AsyncStream.makeStream(of: Int32.self)
        process.terminationHandler = {
            exit.yield($0.terminationStatus)
            exit.finish()
        }

        do {
            try process.run()
        } catch {
            self.error = error.localizedDescription
            return
        }
        loginProcess = process
        defer { loginProcess = nil }

        var output = ""
        do {
            for try await line in pipe.fileHandleForReading.bytes.lines {
                output += line + "\n"
                if case .signedOut = auth,
                   let match = line.firstMatch(of: /one-time code: ([A-Z0-9]{4}-[A-Z0-9]{4})/) {
                    auth = .pending(code: String(match.1))
                }
            }
        } catch {
            output += error.localizedDescription
        }

        var status: Int32 = 1
        for await code in exits { status = code }
        if status == 0 {
            await loadLogin()
        } else {
            error = output.trimmingCharacters(in: .whitespacesAndNewlines)
            auth = .signedOut
        }
    }

    func review(repo: String, row: PRRow, detail: PRDetail) async {
        let key = "\(repo)#\(row.number)"
        guard reviews[key] != .running else { return }
        let model = UserDefaults.standard.string(forKey: "classificationModel") ?? ""
        guard !model.isEmpty, let apiKey = Keychain.read(OpenRouter.keyAccount), !apiKey.isEmpty else {
            reviews[key] = .failed("Set the OpenRouter API key and the classification model in Settings.")
            return
        }
        reviews[key] = .running
        log.info("review \(key, privacy: .public) with \(model, privacy: .public)")
        do {
            async let diff = GH.diff(repo: repo, number: row.number)
            async let agents = GH.agentsFile(repo: repo, ref: detail.baseRefName)
            let input = ReviewInput(
                repo: repo, number: row.number, title: row.title,
                baseRefName: detail.baseRefName, headRefName: detail.headRefName,
                body: detail.body, agents: try await agents, diff: try await diff
            )
            let prompt = ReviewPrompt.user(input)
            let body = try ReviewPrompt.requestBody(model: model, user: prompt.text)
            let review = try await OpenRouter.review(body, apiKey: apiKey)
            reviews[key] = .done(ReviewResult(review: review, model: model, date: .now, prUpdatedAt: row.updatedAt, diffTruncated: prompt.truncated))
            log.info("review \(key, privacy: .public) done: \(review.risk.rawValue, privacy: .public), \(review.findings.count) findings")
        } catch {
            log.error("review \(key, privacy: .public) failed: \(error.localizedDescription, privacy: .public)")
            reviews[key] = .failed(error.localizedDescription)
        }
    }

    func loadRepositories() async {
        guard repositories.isEmpty else { return }
        do {
            repositories = try await GH.repositories()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func poll(repo: String) async {
        guard case .signedIn(let login) = auth else { return }
        rows = []
        lastRefresh = nil
        error = nil
        while !Task.isCancelled {
            do {
                let fetched = try toRows(await GH.reviewRequests(repo: repo, login: login), login: login)
                // GH.run does not stop on cancellation, so a fetch for an old repository must not write rows.
                guard !Task.isCancelled else { return }
                log.info("fetch \(repo, privacy: .public): \(fetched.count) rows")
                rows = fetched
                lastRefresh = .now
                error = nil
            } catch {
                guard !Task.isCancelled else { return }
                log.error("fetch \(repo, privacy: .public) failed: \(error.localizedDescription, privacy: .public)")
                self.error = error.localizedDescription
            }
            try? await Task.sleep(for: .seconds(30))
        }
    }

    func stopLogin() {
        loginProcess?.terminate()
    }

    private func loadLogin() async {
        do {
            auth = .signedIn(login: try await GH.login())
        } catch {
            self.error = error.localizedDescription
            auth = .signedOut
        }
    }
}
