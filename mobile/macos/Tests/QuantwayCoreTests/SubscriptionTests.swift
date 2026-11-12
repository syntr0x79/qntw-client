import XCTest
@testable import QuantwayCore

final class SubscriptionTests: XCTestCase {
    func testDeviceLinkExtractsOriginAndToken() throws {
        let parsed = try SubscriptionLink("https://qntw.link/sub/abcdefghijklmnop?name=desktop")
        XCTAssertEqual(parsed.origin.absoluteString, "https://qntw.link")
        XCTAssertEqual(parsed.token, "abcdefghijklmnop")
    }
    func testRejectsUnsafeLinks() {
        for link in ["http://qntw.link/sub/abcdefghijklmnop", "https://a:b@qntw.link/sub/abcdefghijklmnop", "https://qntw.link/sub/short", "https://qntw.link/sub/abcdefghijklmnop#fragment"] {
            XCTAssertThrowsError(try SubscriptionLink(link))
        }
    }
    func testServerProfileDecoding() throws {
        let data = Data(#"{"version":1,"profiles":[{"job_id":"job","route_id":"route","label":"Germany","engine":"xray","protocol":"vless","transport":"xhttp"}]}"#.utf8)
        let response = try JSONDecoder().decode(ProfileResponse.self, from: data)
        XCTAssertEqual(response.profiles.first?.protocolName, "vless")
        XCTAssertEqual(response.profiles.first?.id, "job")
    }
}

private final class StubURLProtocol: URLProtocol {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let body = "vless://11111111-2222-4333-8444-555555555555@127.0.0.1:443?encryption=none&security=none&type=tcp#Example"
        let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

extension SubscriptionTests {
    func testExternalFeedUsesSharedEngineParser() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [StubURLProtocol.self]
        let client = ExternalSubscriptionClient(session: URLSession(configuration: configuration))
        let profiles = try await client.fetch("https://example.org/feed")
        XCTAssertEqual(profiles.count, 1)
        XCTAssertEqual(profiles.first?.engine, "xray")
        XCTAssertEqual(profiles.first?.label, "Example")
    }
}
