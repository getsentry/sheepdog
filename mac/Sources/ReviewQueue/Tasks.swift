import Foundation
import OSLog

private let log = Logger(subsystem: "com.alwx.reviewqueue", category: "tasks")

// One kind of background work. Each kind is its own type and reports its result to the model itself.
@MainActor
protocol TaskWork {
    var title: String { get }
    var subtitle: String { get }
    // Called inside a cancellable Swift task. The work must stop soon after cancellation.
    func run(in model: AppModel) async
}

@MainActor
@Observable
final class AppTask: Identifiable {
    let id = UUID()
    let work: any TaskWork
    let startedAt = Date.now
    private(set) var handle: Task<Void, Never>?

    init(work: any TaskWork) {
        self.work = work
    }

    func cancel() {
        log.info("cancel \(self.work.title, privacy: .public)")
        handle?.cancel()
    }

    fileprivate func start(in model: AppModel) {
        handle = Task {
            await work.run(in: model)
            model.tasks.removeAll { $0.id == id }
        }
    }
}

extension AppModel {
    @discardableResult
    func start(_ work: any TaskWork) -> AppTask {
        let task = AppTask(work: work)
        tasks.append(task)
        log.info("start \(work.title, privacy: .public)")
        task.start(in: self)
        return task
    }
}

struct ReviewWork: TaskWork {
    let repo: String
    let row: PRRow
    let detail: PRDetail

    static func key(repo: String, number: Int) -> String { "\(repo)#\(number)" }

    var key: String { Self.key(repo: repo, number: row.number) }
    var title: String { "AI review of #\(row.number)" }
    var subtitle: String { repo }

    func run(in model: AppModel) async {
        let previous = model.reviews[key]
        let modelID = UserDefaults.standard.string(forKey: "classificationModel") ?? ""
        let apiKey: String
        do {
            apiKey = try Keychain.read(OpenRouter.keyAccount) ?? ""
        } catch {
            model.reviews[key] = .failed(error.localizedDescription)
            return
        }
        guard !modelID.isEmpty, !apiKey.isEmpty else {
            model.reviews[key] = .failed("Set the OpenRouter API key and the classification model in Settings.")
            return
        }
        model.reviews[key] = .running
        do {
            async let diff = GH.diff(repo: repo, number: row.number)
            async let agents = GH.agentsFile(repo: repo, ref: detail.baseRefName)
            let input = ReviewInput(
                repo: repo, number: row.number, title: row.title,
                baseRefName: detail.baseRefName, headRefName: detail.headRefName,
                body: detail.body, agents: try await agents, diff: try await diff
            )
            try Task.checkCancellation()
            let prompt = ReviewPrompt.user(input)
            let body = try ReviewPrompt.requestBody(model: modelID, user: prompt.text)
            let review = try await OpenRouter.review(body, apiKey: apiKey)
            try Task.checkCancellation()
            model.reviews[key] = .done(ReviewResult(review: review, model: modelID, date: .now, prUpdatedAt: row.updatedAt, diffTruncated: prompt.truncated))
            log.info("review \(key, privacy: .public) done: \(review.risk.rawValue, privacy: .public), \(review.findings.count) findings")
        } catch {
            // URLSession throws URLError.cancelled, not CancellationError, so the task state decides.
            if Task.isCancelled {
                model.reviews[key] = previous
                log.info("review \(key, privacy: .public) cancelled")
            } else {
                model.reviews[key] = .failed(error.localizedDescription)
                log.error("review \(key, privacy: .public) failed: \(error.localizedDescription, privacy: .public)")
            }
        }
    }
}
