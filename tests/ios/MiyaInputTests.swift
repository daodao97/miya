import XCTest

final class MiyaInputTests: XCTestCase {
    private func openComposer(_ sample: String) -> (XCUIApplication, XCUIElement) {
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "com.daodao.miya.inputtest")
        app.terminate()
        app.launchEnvironment["MIYA_INPUT_SAMPLE"] = sample
        app.launch()
        let field = app.descendants(matching: .any)["消息"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10), app.debugDescription)
        field.tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5))
        XCTAssertEqual(field.elementType, .textView, "The real rich composer must expose a native editing proxy")
        return (app, field)
    }

    private func action(_ name: String, _ chinese: String, in app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label IN %@", [name, chinese])).firstMatch
    }

    private func tapAction(_ name: String, _ chinese: String, in app: XCUIApplication) {
        for _ in 0..<4 {
            let item = action(name, chinese, in: app)
            if item.exists && item.isHittable { item.tap(); return }
            let next = app.buttons.matching(NSPredicate(format: "label IN %@", ["Forward", "下一页"])).firstMatch
            if next.exists { next.tap() }
            else if item.waitForExistence(timeout: 3) && item.isHittable { item.tap(); return }
        }
        XCTFail("Missing native action \(name): \(app.debugDescription)")
    }

    func testActualComposerCaretDrag() {
        let (app, field) = openComposer("description")
        let start = field.coordinate(withNormalizedOffset: CGVector(dx: 0, dy: 0.5)).withOffset(CGVector(dx: 65, dy: 0))
        let beginning = field.coordinate(withNormalizedOffset: CGVector(dx: 0, dy: 0.5)).withOffset(CGVector(dx: 1, dy: 0))
        start.press(forDuration: 1, thenDragTo: beginning, withVelocity: .slow, thenHoldForDuration: 0.2)
        app.typeText("X")
        XCTAssertEqual(field.value as? String, "Xdescription")
    }

    func testActualComposerNativePaste() {
        let sample = "description"
        let (app, field) = openComposer(sample)
        let middle = field.coordinate(withNormalizedOffset: CGVector(dx: 0.4, dy: 0.5))
        middle.press(forDuration: 1)
        tapAction("Select All", "全选", in: app)
        tapAction("Copy", "复制", in: app)
        app.typeText("draft")
        XCTAssertEqual(field.value as? String, "draft")
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).press(forDuration: 1)
        XCTAssertTrue(action("Paste", "粘贴", in: app).waitForExistence(timeout: 5), app.debugDescription)
        tapAction("Paste", "粘贴", in: app)
        XCTAssertEqual(field.value as? String, "draft" + sample)
    }

    func testActualComposerSelectionHandle() throws {
        let (app, field) = openComposer("description")
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.4, dy: 0.5)).press(forDuration: 1)
        tapAction("Select All", "全选", in: app)
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
        top.press(forDuration: 1, thenDragTo: next, withVelocity: .slow, thenHoldForDuration: 0.2)
        app.typeText("Y")
        XCTAssertEqual(field.value as? String, "alpha bravo\nY中文 👋 delta")
    }

    func testActualComposerReferenceNativePaste() {
        let ref = "before @[引用](miya://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/thread/codex/ref) after"
        let (app, field) = openComposer(ref)
        XCTAssertEqual(field.value as? String, "before \u{fffc} after")
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.4, dy: 0.5)).press(forDuration: 1)
        tapAction("Select All", "全选", in: app)
        tapAction("Copy", "复制", in: app)
        app.typeText("draft")
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).press(forDuration: 1)
        tapAction("Paste", "粘贴", in: app)
        XCTAssertEqual(field.value as? String, "draftbefore \u{fffc} after", "Native paste lost the inline conversation reference")
    }

}
