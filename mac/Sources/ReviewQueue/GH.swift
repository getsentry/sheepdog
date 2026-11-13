import Foundation

struct GHError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

enum GH {
    // Apps started from Finder get an empty launchd PATH, so the Homebrew locations are checked too.
    static let executable: String? = {
        let path = ProcessInfo.processInfo.environment["PATH"] ?? ""
        let dirs = path.split(separator: ":").map(String.init) + ["/opt/homebrew/bin", "/usr/local/bin"]
        return dirs.map { "\($0)/gh" }.first { FileManager.default.isExecutableFile(atPath: $0) }
    }()

    static func run(_ args: [String]) async throws -> Data {
        guard let executable else {
            throw GHError(message: #"gh CLI not found. Install it with "brew install gh"."#)
        }
        let process = Process()
        process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = args
        process.standardInput = FileHandle.nullDevice
        let out = Pipe()
        let err = Pipe()
        process.standardOutput = out
        process.standardError = err
        try process.run()

        async let stderr = readToEnd(err.fileHandleForReading)
        let stdout = await readToEnd(out.fileHandleForReading)
        let errData = await stderr
        process.waitUntilExit()

        guard process.terminationStatus == 0 else {
            let text = String(decoding: errData, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
            throw GHError(message: text.isEmpty ? "gh exited with code \(process.terminationStatus)" : text)
        }
        return stdout
    }

    private static func readToEnd(_ handle: FileHandle) async -> Data {
        await withCheckedContinuation { continuation in
            DispatchQueue.global().async {
                continuation.resume(returning: handle.readDataToEndOfFile())
            }
        }
    }

    static func authStatus() async -> Bool {
        (try? await run(["auth", "status", "--hostname", "github.com"])) != nil
    }

    static func login() async throws -> String {
        let data = try await run(["api", "user", "-q", ".login"])
        return String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
    }

    static let repositoriesQuery = """
        query($endCursor: String) {
          viewer {
            repositories(first: 100, after: $endCursor, affiliations: [OWNER, COLLABORATOR, ORGANIZATION_MEMBER], ownerAffiliations: [OWNER, COLLABORATOR, ORGANIZATION_MEMBER], isArchived: false, orderBy: {field: PUSHED_AT, direction: DESC}) {
              nodes { nameWithOwner }
              pageInfo { hasNextPage endCursor }
            }
          }
        }
        """

    static func repositories() async throws -> [String] {
        let data = try await run([
            "api", "graphql", "--paginate",
            "-f", "query=\(repositoriesQuery)",
            "-q", ".data.viewer.repositories.nodes[].nameWithOwner",
        ])
        return String(decoding: data, as: UTF8.self).split(separator: "\n").map(String.init)
    }

    static let reviewRequestsQuery = """
        query($q: String!, $owner: String!, $login: String!) {
          search(query: $q, type: ISSUE, first: 100) {
            nodes {
              ... on PullRequest {
                number title url isDraft updatedAt additions deletions
                author { login }
                reviewRequests(first: 50) {
                  nodes { requestedReviewer {
                    __typename
                    ... on User { login }
                    ... on Team { combinedSlug name }
                  } }
                }
              }
            }
          }
          repositoryOwner(login: $owner) {
            ... on Organization {
              teams(first: 100, userLogins: [$login]) { nodes { combinedSlug } }
            }
          }
        }
        """

    static func pullRequest(repo: String, number: Int) async throws -> PRDetail {
        try PRDetail.decode(await run(["pr", "view", String(number), "--repo", repo, "--json", PRDetail.fields]))
    }

    static func reviewRequests(repo: String, login: String) async throws -> Data {
        let owner = repo.split(separator: "/").first.map(String.init) ?? repo
        return try await run([
            "api", "graphql",
            "-f", "query=\(reviewRequestsQuery)",
            "-f", "q=is:pr is:open archived:false repo:\(repo) review-requested:@me",
            "-f", "owner=\(owner)",
            "-f", "login=\(login)",
        ])
    }
}
