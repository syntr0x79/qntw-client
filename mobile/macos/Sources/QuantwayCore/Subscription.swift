import Foundation
import QuantwayEngine

public enum SubscriptionError: LocalizedError {
    case invalidLink, unsupportedLink, server(Int), invalidResponse
    public var errorDescription: String? {
        switch self {
        case .invalidLink: return "Укажите полную HTTPS-ссылку на подписку."
        case .unsupportedLink: return "Поддержка сторонних подписок ещё не готова."
        case .server(let status): return "Сервис ответил с ошибкой \(status)."
        case .invalidResponse: return "Не удалось прочитать ответ сервиса."
        }
    }
}

/// A device token grants access to its configuration; it is not an account session.
public struct SubscriptionLink {
    public let origin: URL
    public let token: String

    public init(_ text: String) throws {
        guard let c = URLComponents(string: text.trimmingCharacters(in: .whitespacesAndNewlines)),
              c.scheme?.lowercased() == "https", let host = c.host, !host.isEmpty,
              c.user == nil, c.password == nil, c.fragment == nil,
              c.percentEncodedPath.range(of: "^/sub/[A-Za-z0-9_-]{16,256}/?$", options: .regularExpression) != nil else {
            throw SubscriptionError.invalidLink
        }
        let pieces = c.path.split(separator: "/")
        guard pieces.count == 2 else { throw SubscriptionError.invalidLink }
        var origin = URLComponents()
        origin.scheme = "https"; origin.host = host; origin.port = c.port
        guard let url = origin.url else { throw SubscriptionError.invalidLink }
        self.origin = url
        self.token = String(pieces[1])
    }
}

public struct Profile: Decodable, Identifiable {
    public let jobID: String
    public let routeID: String
    public let label: String
    public let engine: String
    public let protocolName: String?
    public let transport: String?
    public var id: String { jobID }
    enum CodingKeys: String, CodingKey {
        case jobID = "job_id", routeID = "route_id", label, engine
        case protocolName = "protocol", transport
    }
}

public struct ProfileResponse: Decodable {
    public let version: Int
    public let profiles: [Profile]
}

public final class ProfileClient {
    private let session: URLSession
    public init(session: URLSession = .shared) { self.session = session }
    public func fetch(_ link: SubscriptionLink) async throws -> [Profile] {
        let url = link.origin.appendingPathComponent("api/mobile/v1/profiles")
        var request = URLRequest(url: url)
        request.setValue("Bearer \(link.token)", forHTTPHeaderField: "Authorization")
        request.setValue("QuantwayMac/0.1", forHTTPHeaderField: "User-Agent")
        request.timeoutInterval = 20
        let (data, response) = try await session.data(for: request, delegate: NoRedirects())
        guard let http = response as? HTTPURLResponse else { throw SubscriptionError.invalidResponse }
        guard (200..<300).contains(http.statusCode) else { throw SubscriptionError.server(http.statusCode) }
        guard data.count <= 1_048_576 else { throw SubscriptionError.invalidResponse }
        let decoded = try JSONDecoder().decode(ProfileResponse.self, from: data)
        guard decoded.version == 1 else { throw SubscriptionError.invalidResponse }
        return decoded.profiles
    }
}

private final class NoRedirects: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}

/// Third-party feeds use the same parser and supported protocols as Android.
public final class ExternalSubscriptionClient {
    private let session: URLSession
    public init(session: URLSession = .shared) { self.session = session }
    public func fetch(_ text: String) async throws -> [Profile] {
        guard let parts = URLComponents(string: text.trimmingCharacters(in: .whitespacesAndNewlines)),
              parts.scheme?.lowercased() == "https", parts.host?.isEmpty == false,
              parts.user == nil, parts.password == nil, parts.fragment == nil, let url = parts.url else {
            throw SubscriptionError.invalidLink
        }
        var request = URLRequest(url: url)
        request.timeoutInterval = 20
        request.setValue("QuantwayMac/0.1", forHTTPHeaderField: "User-Agent")
        let (data, response) = try await session.data(for: request, delegate: NoRedirects())
        guard let http = response as? HTTPURLResponse else { throw SubscriptionError.invalidResponse }
        guard (200..<300).contains(http.statusCode) else { throw SubscriptionError.server(http.statusCode) }
        guard data.count <= 1_048_576, let body = String(data: data, encoding: .utf8) else {
            throw SubscriptionError.invalidResponse
        }
        var importError: NSError?
        let parsed = EngineImportSubscription(body, &importError)
        if importError != nil { throw SubscriptionError.invalidResponse }
        let decoded = try JSONDecoder().decode(ProfileResponse.self, from: Data(parsed.utf8))
        guard decoded.version == 1 else { throw SubscriptionError.invalidResponse }
        return decoded.profiles
    }
}
