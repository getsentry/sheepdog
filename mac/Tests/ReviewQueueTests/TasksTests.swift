import Foundation
import Testing
@testable import ReviewQueue

@MainActor
private final class Probe {
    var finished = false
    var cancelled = false
}

private struct SleepWork: TaskWork {
    let probe: Probe
    var title: String { "Sleep" }
    var subtitle: String { "test" }

    func run(in model: AppModel) async {
        do {
            try await Task.sleep(for: .seconds(30))
            probe.finished = true
        } catch {
            probe.cancelled = Task.isCancelled
        }
    }
}

private struct QuickWork: TaskWork {
    let probe: Probe
    var title: String { "Quick" }
    var subtitle: String { "test" }

    func run(in model: AppModel) async {
        probe.finished = true
    }
}

@MainActor
@Test func taskIsListedUntilItFinishes() async {
    let model = AppModel()
    let probe = Probe()
    let task = model.start(QuickWork(probe: probe))
    #expect(model.tasks.map(\.id) == [task.id])
    await task.handle?.value
    #expect(probe.finished)
    #expect(model.tasks.isEmpty)
}

@MainActor
@Test func cancelStopsTheWorkAndRemovesTheTask() async {
    let model = AppModel()
    let probe = Probe()
    let other = model.start(SleepWork(probe: Probe()))
    let task = model.start(SleepWork(probe: probe))
    #expect(model.tasks.count == 2)
    task.cancel()
    await task.handle?.value
    #expect(probe.cancelled)
    #expect(!probe.finished)
    #expect(model.tasks.map(\.id) == [other.id])
    other.cancel()
    await other.handle?.value
    #expect(model.tasks.isEmpty)
}
