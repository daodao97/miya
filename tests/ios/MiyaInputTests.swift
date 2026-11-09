import XCTest

final class MiyaInputTests: XCTestCase {
    private func openComposer(_ sample: String, voice: Bool = false, focus: Bool = true) -> (XCUIApplication, XCUIElement) {
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "com.daodao.miya.inputtest")
        app.terminate()
        app.launchEnvironment["MIYA_INPUT_SAMPLE"] = sample
        if voice { app.launchEnvironment["MIYA_INPUT_VOICE_TEST"] = "1" }
        app.launch()
        let field = app.descendants(matching: .any).matching(NSPredicate(format: "label IN %@", ["Message", "消息"])).firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10), app.debugDescription)
        if focus {
            // Tap text rather than the center, which can be a conversation tag.
            field.coordinate(withNormalizedOffset: CGVector(dx: 0, dy: 0.5)).withOffset(CGVector(dx: 1, dy: 0)).tap()
            XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5))
            XCTAssertEqual(field.elementType, .textView, "The real rich composer must expose a native editing proxy")
        }
        return (app, field)
    }

    private func action(_ name: String, _ chinese: String, in app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label IN %@", [name, chinese])).firstMatch
    }

    // The fixture exposes the shared editor commands outside the composer;
    // the production app has no hint row or edit icon below its input.
    private func tapAction(_ name: String, _ chinese: String, in app: XCUIApplication) {
        for _ in 0..<4 {
            let item = action(name, chinese, in: app)
            if item.exists && item.isHittable { item.tap(); return }
            if name != "Edit text", action("Edit text", "编辑文字", in: app).isHittable {
                action("Edit text", "编辑文字", in: app).tap()
            } else if item.waitForExistence(timeout: 3) && item.isHittable { item.tap(); return }
        }
        XCTFail("Missing editor action \(name): \(app.debugDescription)")
    }

    func testActualComposerCaretTap() {
        let (app, field) = openComposer("description")
        let start = field.coordinate(withNormalizedOffset: CGVector(dx: 0, dy: 0.5)).withOffset(CGVector(dx: 65, dy: 0))
        let beginning = field.coordinate(withNormalizedOffset: CGVector(dx: 0, dy: 0.5)).withOffset(CGVector(dx: 1, dy: 0))
        start.tap()
        beginning.tap()
        app.typeText("X")
        XCTAssertEqual(field.value as? String, "Xdescription")
    }

    func testActualComposerNativePaste() {
        let sample = "description"
        let (app, field) = openComposer(sample)
        tapAction("Edit text", "编辑文字", in: app)
        tapAction("Select all", "全选", in: app)
        tapAction("Copy", "复制", in: app)
        app.typeText("draft")
        XCTAssertEqual(field.value as? String, "draft")
        tapAction("Edit text", "编辑文字", in: app)
        tapAction("Paste", "粘贴", in: app)
        XCTAssertEqual(field.value as? String, "draft" + sample)
    }

    func testActualComposerSelectionHandle() throws {
        let (app, field) = openComposer("description")
        tapAction("Edit text", "编辑文字", in: app)
        tapAction("Select all", "全选", in: app)
        let handles = app.descendants(matching: .any).matching(NSPredicate(format: "identifier IN %@", ["Selection end handle", "com.apple.text.grabber.trailing"]))
        XCTAssertTrue(handles.firstMatch.waitForExistence(timeout: 5), app.debugDescription)
        let handle = try XCTUnwrap(handles.allElementsBoundByIndex.first { $0.frame.width > 0 && $0.frame.height > 0 && $0.isHittable }, app.debugDescription)
        let earlier = field.coordinate(withNormalizedOffset: CGVector(dx: 0, dy: 0.5)).withOffset(CGVector(dx: 27, dy: 0))
        handle.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).press(forDuration: 0.1, thenDragTo: earlier, withVelocity: .slow, thenHoldForDuration: 0.2)
        app.typeText("Z")
        let value = field.value as? String ?? ""
        XCTAssertTrue(value.hasPrefix("Z") && value.hasSuffix("tion"), "Handle did not shorten the actual composer selection: \(value)")
    }

    func testActualComposerMultilineCaret() {
        let sample = "alpha bravo\n中文 👋 delta"
        let (app, field) = openComposer(sample)
        let top = field.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: 2, dy: 10))
        let next = field.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: 2, dy: 35))
        top.tap()
        next.tap()
        app.typeText("Y")
        XCTAssertEqual(field.value as? String, "alpha bravo\nY中文 👋 delta")
    }

    func testActualComposerReferenceNativePaste() {
        let ref = "before @[引用](miya://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/thread/codex/ref) after"
        let (app, field) = openComposer(ref)
        XCTAssertEqual(field.value as? String, "before \u{fffc} after")
        tapAction("Edit text", "编辑文字", in: app)
        tapAction("Select all", "全选", in: app)
        tapAction("Copy", "复制", in: app)
        app.typeText("draft")
        tapAction("Edit text", "编辑文字", in: app)
        tapAction("Paste", "粘贴", in: app)
        XCTAssertEqual(field.value as? String, "draftbefore \u{fffc} after", "Native paste lost the inline conversation reference")
    }

    func testActualComposerHoldInsertsVoiceAfterRelease() {
        let (app, field) = openComposer("draft", voice: true, focus: false)
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.7, dy: 0.5)).press(forDuration: 1)
        let result = NSPredicate(format: "value CONTAINS %@", "voice transcription test")
        expectation(for: result, evaluatedWith: field)
        waitForExpectations(timeout: 8)
        XCTAssertTrue((field.value as? String ?? "").contains("draft"), "Voice input must retain the existing draft")
        XCTAssertFalse(action("Cancel voice input", "取消语音输入", in: app).exists)
    }

    func testActualComposerFocusedHoldKeepsNativePaste() {
        let (app, field) = openComposer("description", voice: true)
        tapAction("Edit text", "编辑文字", in: app)
        tapAction("Select all", "全选", in: app)
        tapAction("Copy", "复制", in: app)
        app.typeText("draft")
        field.coordinate(withNormalizedOffset: CGVector(dx: 0, dy: 0.5)).withOffset(CGVector(dx: 10, dy: 0)).press(forDuration: 1)
        XCTAssertEqual(field.value as? String, "draft", "Holding a focused editor must not start voice input")
        XCTAssertFalse(action("Cancel voice input", "取消语音输入", in: app).exists)
        let paste = action("Paste", "粘贴", in: app)
        XCTAssertTrue(paste.waitForExistence(timeout: 5), app.debugDescription)
        paste.tap()
        XCTAssertTrue((field.value as? String ?? "").contains("description"), "Native paste must insert the copied text")
    }

}
