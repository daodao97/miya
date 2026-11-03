import Foundation
import UIKit
import WebKit
import QuickLook

@_silgen_name("goIOSSystemResult") private func filesResult(_ token: UInt64, _ value: UnsafePointer<CChar>, _ code: Int32, _ message: UnsafePointer<CChar>)

// Miya's file and Web service viewer implements MyGo's optional in-app browser hook.
// It has an isolated web data store and no JavaScript-to-native app bridge.
@MainActor private final class MiyaFiles: UIViewController, WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate, QLPreviewControllerDataSource, UIAdaptivePresentationControllerDelegate {
    static var sessions: [UInt64: MiyaFiles] = [:]
    static var canceled: Set<UInt64> = []
    let token: UInt64
    let initial: URL
    let cache: URL
    var isWebService: Bool { initial.path.hasPrefix("/.miya-web-open/") }
    var viewerTitle: String { isWebService ? "Web 服务" : "项目文件" }
    var web: WKWebView!
    var downloads: [ObjectIdentifier: (WKDownload, URL?)] = [:]
    var preview: URL?
    var back: UIBarButtonItem!
    var forward: UIBarButtonItem!
    var save: UIBarButtonItem!
    var finished = false

    init(_ token: UInt64, _ url: URL) {
        self.token = token; initial = url
        cache = FileManager.default.temporaryDirectory.appendingPathComponent("miya-files-" + UUID().uuidString, isDirectory: true)
        super.init(nibName: nil, bundle: nil)
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) is unavailable") }
    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .systemBackground
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .nonPersistent()
        web = WKWebView(frame: .zero, configuration: configuration)
        web.navigationDelegate = self; web.uiDelegate = self
        web.allowsBackForwardNavigationGestures = true
        web.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(web)
        NSLayoutConstraint.activate([web.leadingAnchor.constraint(equalTo: view.leadingAnchor), web.trailingAnchor.constraint(equalTo: view.trailingAnchor), web.topAnchor.constraint(equalTo: view.topAnchor), web.bottomAnchor.constraint(equalTo: view.bottomAnchor)])
        navigationItem.title = viewerTitle
        navigationItem.leftBarButtonItem = UIBarButtonItem(barButtonSystemItem: .done, target: self, action: #selector(close))
        navigationItem.rightBarButtonItem = UIBarButtonItem(title: isWebService ? "首页" : "目录", style: .plain, target: self, action: #selector(home))
        back = UIBarButtonItem(image: UIImage(systemName: "chevron.left"), style: .plain, target: self, action: #selector(goBack))
        back.accessibilityLabel = "后退"
        forward = UIBarButtonItem(image: UIImage(systemName: "chevron.right"), style: .plain, target: self, action: #selector(goForward))
        forward.accessibilityLabel = "前进"
        save = UIBarButtonItem(barButtonSystemItem: .action, target: self, action: #selector(downloadCurrent))
        save.accessibilityLabel = "下载或分享当前文件"
        toolbarItems = [back, .flexibleSpace(), forward, .flexibleSpace(), UIBarButtonItem(barButtonSystemItem: .refresh, target: self, action: #selector(reload)), .flexibleSpace(), save]
        if isWebService { toolbarItems = Array(toolbarItems!.dropLast(2)) }
        navigationController?.setToolbarHidden(false, animated: false)
        web.load(URLRequest(url: initial))
    }
    @objc func goBack() { web.goBack() }
    @objc func goForward() { web.goForward() }
    @objc func reload() { web.reload() }
    @objc func home() { web.load(URLRequest(url: initial)) }
    @objc func downloadCurrent() {
        guard let url = web.url, var parts = URLComponents(url: url, resolvingAgainstBaseURL: false) else { return }
        parts.queryItems = (parts.queryItems ?? []).filter { $0.name != "download" } + [URLQueryItem(name: "download", value: "1")]
        if let url = parts.url { web.load(URLRequest(url: url)) }
    }
    func controls() {
        back.isEnabled = web.canGoBack; forward.isEnabled = web.canGoForward
        save.isEnabled = web.url?.path.hasSuffix("/") == false
        navigationItem.title = web.title?.isEmpty == false ? web.title : viewerTitle
    }
    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) { controls() }
    func webView(_ webView: WKWebView, didCommit navigation: WKNavigation!) { controls() }
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        let scheme = action.request.url?.scheme ?? ""
        guard ["http", "https", "about", "blob", "data"].contains(scheme) else { decisionHandler(.cancel); return }
        decisionHandler(action.shouldPerformDownload ? .download : .allow)
    }
    func webView(_ webView: WKWebView, decidePolicyFor response: WKNavigationResponse, decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void) {
        let attachment = (response.response as? HTTPURLResponse)?.value(forHTTPHeaderField: "Content-Disposition")?.lowercased().contains("attachment") == true
        decisionHandler(!response.canShowMIMEType || attachment ? .download : .allow)
    }
    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) { track(download) }
    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) { track(download) }
    func track(_ download: WKDownload) { downloads[ObjectIdentifier(download)] = (download, nil); download.delegate = self }
    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration, for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        if action.targetFrame == nil { webView.load(action.request) }; return nil
    }
    func download(_ download: WKDownload, decideDestinationUsing response: URLResponse, suggestedFilename: String, completionHandler: @escaping (URL?) -> Void) {
        let directory = cache.appendingPathComponent(UUID().uuidString, isDirectory: true)
        do {
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            let name = (suggestedFilename as NSString).lastPathComponent
            let file = directory.appendingPathComponent(name.isEmpty || name == "." || name == ".." ? "download" : name)
            downloads[ObjectIdentifier(download)] = (download, file)
            completionHandler(file)
        } catch { completionHandler(nil); showError(error.localizedDescription) }
    }
    func downloadDidFinish(_ download: WKDownload) {
        guard let (_, file) = downloads.removeValue(forKey: ObjectIdentifier(download)), let file else { return }
        preview = file
        guard presentedViewController == nil else { return }
        if QLPreviewController.canPreview(file as NSURL) {
            let controller = QLPreviewController(); controller.dataSource = self
            present(controller, animated: true)
        } else {
            let controller = UIActivityViewController(activityItems: [file], applicationActivities: nil)
            controller.popoverPresentationController?.barButtonItem = save
            present(controller, animated: true)
        }
    }
    func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {
        if let (_, file) = downloads.removeValue(forKey: ObjectIdentifier(download)), let file { try? FileManager.default.removeItem(at: file) }
        if !finished { showError("下载失败：" + error.localizedDescription) }
    }
    func numberOfPreviewItems(in controller: QLPreviewController) -> Int { preview == nil ? 0 : 1 }
    func previewController(_ controller: QLPreviewController, previewItemAt index: Int) -> QLPreviewItem { preview! as NSURL }
    func showError(_ message: String) {
        guard presentedViewController == nil else { return }
        let alert = UIAlertController(title: viewerTitle, message: message, preferredStyle: .alert)
        alert.addAction(UIAlertAction(title: "好", style: .default)); present(alert, animated: true)
    }
    @objc func close() { finish(dismiss: true) }
    func presentationControllerDidDismiss(_ presentationController: UIPresentationController) { finish(dismiss: false) }
    func finish(dismiss: Bool) {
        guard !finished else { return }; finished = true
        web?.stopLoading()
        for (download, _) in downloads.values { download.cancel { _ in } }; downloads.removeAll()
        let complete = { [self] in
            Self.sessions.removeValue(forKey: token)
            try? FileManager.default.removeItem(at: cache)
            "null".withCString { value in "".withCString { filesResult(token, value, 0, $0) } }
        }
        if dismiss { navigationController?.dismiss(animated: true, completion: complete) } else { complete() }
    }
    static func open(_ token: UInt64, _ json: String) {
        if canceled.remove(token) != nil { return }
        guard let data = json.data(using: .utf8), let raw = try? JSONDecoder().decode(String.self, from: data), let url = URL(string: raw), ["http", "https"].contains(url.scheme ?? "") else { fail(token, "Invalid file URL"); return }
        let windows = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.filter { $0.activationState == .foregroundActive }.flatMap { $0.windows }
        guard let root = windows.first(where: { $0.isKeyWindow })?.rootViewController, root.presentedViewController == nil else { fail(token, "Another presentation is active"); return }
        let viewer = MiyaFiles(token, url)
        let navigation = UINavigationController(rootViewController: viewer)
        navigation.modalPresentationStyle = .pageSheet
        if let sheet = navigation.sheetPresentationController {
            sheet.detents = [.medium(), .large()]
            sheet.selectedDetentIdentifier = .large
            sheet.prefersGrabberVisible = true
            sheet.prefersScrollingExpandsWhenScrolledToEdge = true
            sheet.prefersEdgeAttachedInCompactHeight = true
        }
        sessions[token] = viewer
        root.present(navigation, animated: true)
        navigation.presentationController?.delegate = viewer
    }
    static func fail(_ token: UInt64, _ message: String) { "null".withCString { value in message.withCString { filesResult(token, value, 2, $0) } } }
}

@_cdecl("mygo_swift_browser_open") func miyaFileBrowserOpen(_ token: UInt64, _ json: UnsafePointer<CChar>) {
    let value = String(cString: json)
    Task { @MainActor in MiyaFiles.open(token, value) }
}
@_cdecl("mygo_swift_browser_cancel") func miyaFileBrowserCancel(_ token: UInt64) {
    Task { @MainActor in
        if let viewer = MiyaFiles.sessions[token] { viewer.close() }
        else { if MiyaFiles.canceled.count > 128 { MiyaFiles.canceled.removeAll() }; MiyaFiles.canceled.insert(token); MiyaFiles.fail(token, "Canceled") }
    }
}
