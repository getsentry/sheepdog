import Foundation
import Observation

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
