import Foundation

struct PRRow: Identifiable, Equatable {
    let number: Int
    let title: String
    let url: URL
    let isDraft: Bool
    let updatedAt: Date
    let author: String
    let reasons: [String]

    var id: Int { number }
}

private struct Response: Decodable {
    let data: ResponseData
}

private struct ResponseData: Decodable {
    let search: Nodes<PullRequest>
    let repositoryOwner: Owner?
}

private struct Nodes<T: Decodable>: Decodable {
    let nodes: [T]
}

private struct Owner: Decodable {
    let teams: Nodes<TeamSlug>?
}

private struct TeamSlug: Decodable {
    let combinedSlug: String
}

private struct PullRequest: Decodable {
    let number: Int
    let title: String
    let url: URL
    let isDraft: Bool
    let updatedAt: Date
    let author: Author?
    let reviewRequests: Nodes<ReviewRequest>
}

private struct Author: Decodable {
    let login: String
}

private struct ReviewRequest: Decodable {
    let requestedReviewer: Reviewer?
}

private struct Reviewer: Decodable {
    let login: String?
    let combinedSlug: String?
    let name: String?
}

func toRows(_ data: Data, login: String) throws -> [PRRow] {
    let decoder = JSONDecoder()
    decoder.dateDecodingStrategy = .iso8601
    let response = try decoder.decode(Response.self, from: data).data
    let myTeams = Set(response.repositoryOwner?.teams?.nodes.map(\.combinedSlug) ?? [])

    return response.search.nodes.map { pr in
        let reasons = pr.reviewRequests.nodes.compactMap { request -> String? in
            guard let reviewer = request.requestedReviewer else { return nil }
            if reviewer.login == login { return "you" }
            if let slug = reviewer.combinedSlug, myTeams.contains(slug) { return reviewer.name }
            return nil
        }
        return PRRow(
            number: pr.number,
            title: pr.title,
            url: pr.url,
            isDraft: pr.isDraft,
            updatedAt: pr.updatedAt,
            author: pr.author?.login ?? "ghost",
            reasons: reasons
        )
    }
    .sorted { $0.updatedAt > $1.updatedAt }
}
