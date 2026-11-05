//go:build darwin && !ios && cgo

#import <Cocoa/Cocoa.h>
#import <ApplicationServices/ApplicationServices.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <ImageIO/ImageIO.h>
#import <IOKit/pwr_mgt/IOPMLib.h>
#include <unistd.h>
#include <string.h>

// Completion can arrive after the five-second deadline. The copied framework
// blocks retain this object, so a late result is freed rather than leaked or
// written through a stack pointer whose Go call has returned.
@interface MiyaDesktopShot : NSObject {
@public NSCondition *condition;
@public CFDataRef jpeg;
@public BOOL finished;
@public int status;
@public NSString *failure;
}
- (void)complete:(CFDataRef)data status:(int)code error:(NSError *)error;
@end
@implementation MiyaDesktopShot
- (id)init { if ((self = [super init])) { condition = [[NSCondition alloc] init]; status = 8; } return self; }
- (void)complete:(CFDataRef)data status:(int)code error:(NSError *)error {
    [condition lock];
    if (!finished) {
        jpeg = data; status = code;
        // Preserve only system error domain/code; descriptions may contain
        // window titles or other user data and never enter the phone response.
        if (error) failure = [[NSString stringWithFormat:@"%@ (%ld)", error.domain, (long)error.code] copy];
        finished = YES; [condition signal];
    }
    else if (data) CFRelease(data);
    [condition unlock];
}
- (void)dealloc { if (jpeg) CFRelease(jpeg); [failure release]; [condition release]; [super dealloc]; }
@end

int miya_desktop_available(void) { if (@available(macOS 14.0, *)) return 1; return 0; }
// Same effect as local input: lights a sleeping display (the lock screen when
// locked) and restarts the idle timer. It changes no settings and nothing is
// held once remote control stops refreshing it. Callers serialize this.
// 1 locked, 0 unlocked, -1 unknown. Reads only the session's public flag.
int miya_desktop_locked(void) { @autoreleasepool {
    CFDictionaryRef session = CGSessionCopyCurrentDictionary();
    if (!session) return -1;
    CFBooleanRef value = CFDictionaryGetValue(session, CFSTR("CGSSessionScreenIsLocked"));
    int locked = value && CFGetTypeID(value) == CFBooleanGetTypeID() && CFBooleanGetValue(value) ? 1 : 0;
    CFRelease(session);
    return locked;
}}
int miya_desktop_wake(void) { @autoreleasepool {
    static IOPMAssertionID activity = kIOPMNullAssertionID;
    CGDirectDisplayID display = CGMainDisplayID();
    BOOL asleep = CGDisplayIsAsleep(display);
    if (IOPMAssertionDeclareUserActivity(CFSTR("Miya remote control"), kIOPMUserActiveLocal, &activity) != kIOReturnSuccess) return 12;
    for (int i = 0; asleep && i < 40 && CGDisplayIsAsleep(display); i++) usleep(50000);
    return 0;
}}
int miya_desktop_authorize(void) { @autoreleasepool {
    if (!miya_desktop_available()) return 3;
    if (!CGPreflightScreenCaptureAccess()) { CGRequestScreenCaptureAccess(); return 1; }
    if (!AXIsProcessTrusted()) {
        NSDictionary *options = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt: @YES};
        AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)options);
        return 2;
    }
    return 0;
} }

static CFDataRef encodeDesktop(CGImageRef image) {
    size_t iw = CGImageGetWidth(image), ih = CGImageGetHeight(image);
    if (!iw || !ih) return NULL;
    double scale = fmin(1.0, 1600.0 / fmax(iw, ih));
    size_t w = fmax(1, iw * scale), h = fmax(1, ih * scale);
    CGColorSpaceRef colors = CGColorSpaceCreateDeviceRGB();
    CGContextRef ctx = CGBitmapContextCreate(NULL, w, h, 8, w * 4, colors, (CGBitmapInfo)kCGImageAlphaNoneSkipLast);
    CGColorSpaceRelease(colors);
    if (!ctx) return NULL;
    CGContextDrawImage(ctx, CGRectMake(0, 0, w, h), image);
    CGImageRef resized = CGBitmapContextCreateImage(ctx);
    CGContextRelease(ctx);
    CFMutableDataRef data = CFDataCreateMutable(NULL, 0);
    CGImageDestinationRef dest = CGImageDestinationCreateWithData(data, CFSTR("public.jpeg"), 1, NULL);
    if (!dest || !resized) { if (dest) CFRelease(dest); if (resized) CGImageRelease(resized); CFRelease(data); return NULL; }
    CGImageDestinationAddImage(dest, resized, (__bridge CFDictionaryRef)@{(__bridge NSString *)kCGImageDestinationLossyCompressionQuality: @0.65});
    BOOL ok = CGImageDestinationFinalize(dest);
    CFRelease(dest); CGImageRelease(resized);
    if (!ok) { CFRelease(data); return NULL; }
    return data;
}

extern BOOL miya_display_matches(CGDirectDisplayID display, NSString *uuid);

static int captureTarget(CGWindowID windowID, pid_t pid, CGDirectDisplayID displayID, NSString *displayUUID, void **bytes, int *size, char **failure) { @autoreleasepool {
    if (!CGPreflightScreenCaptureAccess()) return 1;
    if (@available(macOS 14.0, *)) {
        MiyaDesktopShot *shot = [[MiyaDesktopShot alloc] init];
        [SCShareableContent getShareableContentExcludingDesktopWindows:NO onScreenWindowsOnly:YES completionHandler:^(SCShareableContent *content, NSError *error) {
            if (error || !content) { [shot complete:NULL status:5 error:error]; return; }
            SCContentFilter *filter = nil;
            double width = 0, height = 0;
            if (windowID) {
                for (SCWindow *window in content.windows) {
                    if (window.windowID != windowID || window.owningApplication.processID != pid) continue;
                    filter = [[SCContentFilter alloc] initWithDesktopIndependentWindow:window];
                    width = window.frame.size.width; height = window.frame.size.height;
                    break;
                }
            } else {
                if (displayUUID && !miya_display_matches(displayID, displayUUID)) { [shot complete:NULL status:14 error:nil]; return; }
                for (SCDisplay *display in content.displays) {
                    if (display.displayID != displayID) continue;
                    filter = [[SCContentFilter alloc] initWithDisplay:display excludingWindows:@[]];
                    width = display.width; height = display.height;
                    break;
                }
            }
            if (!filter || width <= 0 || height <= 0) { [filter release]; [shot complete:NULL status:(windowID ? 6 : 13) error:nil]; return; }
            SCStreamConfiguration *config = [[SCStreamConfiguration alloc] init];
            double scale = fmin(1.0, 1600.0 / fmax(width, height));
            config.width = fmax(1, width * scale); config.height = fmax(1, height * scale);
            config.showsCursor = !windowID;
            config.ignoreShadowsSingleWindow = YES;
            config.ignoreGlobalClipSingleWindow = YES;
            [SCScreenshotManager captureImageWithFilter:filter configuration:config completionHandler:^(CGImageRef image, NSError *err) {
                @autoreleasepool {
                    if (err || !image) { [shot complete:NULL status:7 error:err]; return; }
                    CFDataRef jpeg = encodeDesktop(image);
                    [shot complete:jpeg status:(jpeg ? 0 : 9) error:nil];
                }
            }];
            [config release]; [filter release];
        }];
        [shot->condition lock];
        NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:5];
        while (!shot->finished && [shot->condition waitUntilDate:deadline]) {}
        shot->finished = YES;
        int result = shot->status;
        if (shot->failure) *failure = strdup(shot->failure.UTF8String);
        if (shot->jpeg && CFDataGetLength(shot->jpeg) > 2 * 1024 * 1024) result = 10;
        else if (shot->jpeg) {
            *size = (int)CFDataGetLength(shot->jpeg); *bytes = malloc(*size);
            if (*bytes) { memcpy(*bytes, CFDataGetBytePtr(shot->jpeg), *size); result = 0; }
            else result = 11;
        }
        [shot->condition unlock]; [shot release];
        return result;
    }
    return 3;
} }

int miya_desktop_capture(void **bytes, int *size, char **failure) { return captureTarget(0, 0, CGMainDisplayID(), nil, bytes, size, failure); }

int miya_desktop_capture_display(unsigned int display, const char *uuid, void **bytes, int *size, char **failure) { @autoreleasepool {
    NSString *identity = [NSString stringWithUTF8String:uuid];
    if (!miya_display_matches(display, identity)) return 14;
    return captureTarget(0, 0, display, identity, bytes, size, failure);
}}

static NSDictionary *windowInfo(CGWindowID windowID, pid_t pid) {
    NSArray *windows = CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID));
    for (NSDictionary *info in windows) {
        if ([info[(id)kCGWindowNumber] unsignedIntValue] == windowID && [info[(id)kCGWindowOwnerPID] intValue] == pid) return info;
    }
    return nil;
}

int miya_desktop_capture_window(unsigned int windowID, int pid, void **bytes, int *size, char **failure) { @autoreleasepool {
    if (!windowID || !windowInfo(windowID, pid)) return 4;
    return captureTarget(windowID, pid, 0, nil, bytes, size, failure);
} }

int miya_desktop_windows(char **json) { @autoreleasepool {
    if (!miya_desktop_available()) return 3;
    if (!CGPreflightScreenCaptureAccess()) return 1;
    NSArray *windows = CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID));
    NSMutableArray *result = [NSMutableArray array];
    for (NSDictionary *info in windows) {
        // Authorization panels can live above ordinary (layer-zero) windows.
        if ([info[(id)kCGWindowLayer] intValue] < 0 || [info[(id)kCGWindowAlpha] doubleValue] <= 0) continue;
        CGRect bounds;
        if (!CGRectMakeWithDictionaryRepresentation((CFDictionaryRef)info[(id)kCGWindowBounds], &bounds) || bounds.size.width < 80 || bounds.size.height < 40) continue;
        pid_t pid = [info[(id)kCGWindowOwnerPID] intValue];
        NSRunningApplication *app = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
        if ([app.bundleIdentifier isEqualToString:@"com.daodao.miya"]) continue;
        if ([@[@"com.apple.dock",@"com.apple.systemuiserver",@"com.apple.controlcenter",@"com.apple.notificationcenterui",@"com.apple.WindowManager"] containsObject:app.bundleIdentifier ?: @""]) continue;
        NSString *name = app.localizedName ?: info[(id)kCGWindowOwnerName] ?: @"App";
        NSString *title = info[(id)kCGWindowName] ?: name;
        [result addObject:@{@"id": info[(id)kCGWindowNumber], @"pid": @(pid), @"app": name, @"title": title, @"bundleId": app.bundleIdentifier ?: @""}];
        if (result.count >= 100) break;
    }
    NSData *data = [NSJSONSerialization dataWithJSONObject:result options:0 error:NULL];
    if (!data) return 9;
    *json = calloc(data.length + 1, 1);
    if (!*json) return 11;
    memcpy(*json, data.bytes, data.length);
    return 0;
} }

static void postMouse(CGEventType type, CGPoint point, CGMouseButton button, int count) {
    CGEventRef event = CGEventCreateMouseEvent(NULL, type, point, button);
    if (!event) return;
    CGEventSetIntegerValueField(event, kCGMouseEventClickState, count);
    CGEventPost(kCGHIDEventTap, event); CFRelease(event);
}

static BOOL windowIsFront(CGWindowID windowID, pid_t pid, CGPoint point, BOOL pointer);

static int inputAtBounds(CGRect bounds, CGWindowID windowID, pid_t pid, int kind, double x, double y, double tx, double ty, int dx, int dy, const unsigned short *text, int length, int key, int modifier) { @autoreleasepool {
    if (!AXIsProcessTrusted()) return 2;
    CGPoint point = CGPointMake(bounds.origin.x + x * (bounds.size.width - 1), bounds.origin.y + y * (bounds.size.height - 1));
    // Cursor-only motion for trackpad mode; no button state changes.
    if (kind == 7) { postMouse(kCGEventMouseMoved, point, kCGMouseButtonLeft, 0); return 0; }
    if (kind <= 3) {
        BOOL right = kind == 2;
        CGMouseButton button = right ? kCGMouseButtonRight : kCGMouseButtonLeft;
        CGEventType down = right ? kCGEventRightMouseDown : kCGEventLeftMouseDown;
        CGEventType up = right ? kCGEventRightMouseUp : kCGEventLeftMouseUp;
        postMouse(kCGEventMouseMoved, point, button, 0);
        postMouse(down, point, button, 1);
        if (kind == 3) {
            for (int i = 1; i <= 12; i++) {
                CGPoint p = CGPointMake(point.x + (tx-x) * (bounds.size.width-1) * i/12.0, point.y + (ty-y) * (bounds.size.height-1) * i/12.0);
                postMouse(kCGEventLeftMouseDragged, p, button, 1);
                usleep(5000);
            }
            point = CGPointMake(bounds.origin.x + tx*(bounds.size.width-1), bounds.origin.y + ty*(bounds.size.height-1));
        }
        postMouse(up, point, button, 1);
        if (kind == 1) { postMouse(down, point, button, 2); postMouse(up, point, button, 2); }
    } else if (kind == 4) {
        postMouse(kCGEventMouseMoved, point, kCGMouseButtonLeft, 0);
        CGEventRef event = CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitPixel, 2, dy, dx);
        if (event) { CGEventPost(kCGHIDEventTap, event); CFRelease(event); }
    } else {
        CGEventFlags flags = modifier == 1 ? kCGEventFlagMaskCommand : modifier == 2 ? kCGEventFlagMaskControl : modifier == 3 ? kCGEventFlagMaskShift : modifier == 4 ? kCGEventFlagMaskAlternate : 0;
        // A bounded chunk avoids the OS Unicode event's length limit. Keep
        // surrogate pairs together; text is never written to the clipboard.
        for (int at = 0; at < (kind == 5 ? length : 1);) {
            if (windowID && !windowIsFront(windowID, pid, point, NO)) return 4;
            int n = kind == 5 ? MIN(20, length-at) : 1;
            if (kind == 5 && at+n < length && text[at+n-1] >= 0xD800 && text[at+n-1] <= 0xDBFF) n--;
            for (int down = 1; down >= 0; down--) {
                CGEventRef event = CGEventCreateKeyboardEvent(NULL, kind == 5 ? 0 : key, down);
                if (event) {
                    CGEventSetFlags(event, flags);
                    if (kind == 5) CGEventKeyboardSetUnicodeString(event, n, text+at);
                    if (pid) CGEventPostToPid(pid, event); else CGEventPost(kCGHIDEventTap, event);
                    CFRelease(event);
                }
            }
            at += n;
        }
    }
    return 0;
} }

int miya_desktop_input(int kind, double x, double y, double tx, double ty, int dx, int dy, const unsigned short *text, int length, int key, int modifier) {
    return inputAtBounds(CGDisplayBounds(CGMainDisplayID()), 0, 0, kind, x, y, tx, ty, dx, dy, text, length, key, modifier);
}

int miya_desktop_display_input(unsigned int display, const char *uuid, int kind, double x, double y, double tx, double ty, int dx, int dy, const unsigned short *text, int length, int key, int modifier) { @autoreleasepool {
    if (!miya_display_matches(display, [NSString stringWithUTF8String:uuid])) return 14;
    return inputAtBounds(CGDisplayBounds(display), 0, 0, kind, x, y, tx, ty, dx, dy, text, length, key, modifier);
}}

// Match AX windows by their public geometry. There is no public AX window-ID
// accessor. Refuse ambiguity instead of raising a different window of the app.
static AXUIElementRef copyWindowElement(CGRect bounds, pid_t pid) {
    AXUIElementRef app = AXUIElementCreateApplication(pid);
    AXUIElementSetMessagingTimeout(app, 0.2);
    CFArrayRef windows = NULL;
    AXUIElementRef match = NULL;
    if (AXUIElementCopyAttributeValue(app, kAXWindowsAttribute, (CFTypeRef *)&windows) == kAXErrorSuccess) {
        for (CFIndex i = 0; i < CFArrayGetCount(windows); i++) {
            AXUIElementRef window = (AXUIElementRef)CFArrayGetValueAtIndex(windows, i);
            AXValueRef position = NULL, size = NULL;
            CGPoint p; CGSize s;
            BOOL ok = AXUIElementCopyAttributeValue(window, kAXPositionAttribute, (CFTypeRef *)&position) == kAXErrorSuccess &&
                AXUIElementCopyAttributeValue(window, kAXSizeAttribute, (CFTypeRef *)&size) == kAXErrorSuccess &&
                AXValueGetValue(position, kAXValueCGPointType, &p) && AXValueGetValue(size, kAXValueCGSizeType, &s);
            if (position) CFRelease(position); if (size) CFRelease(size);
            if (!ok || fabs(p.x-bounds.origin.x) > 2 || fabs(p.y-bounds.origin.y) > 2 || fabs(s.width-bounds.size.width) > 2 || fabs(s.height-bounds.size.height) > 2) continue;
            if (match) { CFRelease(match); match = NULL; break; }
            match = (AXUIElementRef)CFRetain(window);
        }
        CFRelease(windows);
    }
    CFRelease(app);
    return match;
}

static BOOL focusWindow(NSDictionary *info, CGRect bounds, pid_t pid) {
    AXUIElementRef app = AXUIElementCreateApplication(pid);
    AXUIElementRef match = copyWindowElement(bounds, pid);
    BOOL found = match != NULL;
    if (found) {
        [[NSRunningApplication runningApplicationWithProcessIdentifier:pid] activateWithOptions:NSApplicationActivateIgnoringOtherApps];
        AXUIElementPerformAction(match, kAXRaiseAction);
        AXUIElementSetAttributeValue(app, kAXFocusedWindowAttribute, match);
        CFRelease(match);
    }
    CFRelease(app);
    return found;
}

static NSString *axString(AXUIElementRef element, CFStringRef attribute) {
    CFTypeRef value = NULL;
    AXUIElementCopyAttributeValue(element, attribute, &value);
    NSString *text = value && CFGetTypeID(value) == CFStringGetTypeID() ? [(__bridge NSString *)value copy] : nil;
    if (value) CFRelease(value);
    return [text autorelease];
}

// Read only static labels and button metadata. Never read editable text or
// secure-field values, even when accessibility exposes them.
static void dialogElements(AXUIElementRef element, NSMutableArray *texts, NSMutableArray *buttons, int depth, int *budget) {
    if (depth > 8 || --*budget < 0) return;
    AXUIElementSetMessagingTimeout(element, 0.1);
    NSString *role = axString(element, kAXRoleAttribute);
    if ([role isEqualToString:(__bridge NSString *)kAXTextFieldRole] || [role isEqualToString:(__bridge NSString *)kAXTextAreaRole] || [role isEqualToString:(__bridge NSString *)kAXComboBoxRole]) return;
    // App navigation and document contents are not authorization copy. Dialog
    // labels remain available, while Settings sidebars and browser pages do not
    // become a long, unrelated explanation on the phone.
    if ([role isEqualToString:(__bridge NSString *)kAXScrollAreaRole] || [role isEqualToString:(__bridge NSString *)kAXOutlineRole] || [role isEqualToString:(__bridge NSString *)kAXTableRole] || [role isEqualToString:(__bridge NSString *)kAXToolbarRole] || [role isEqualToString:(__bridge NSString *)kAXMenuBarRole] || [role isEqualToString:@"AXWebArea"]) return;
    if ([role isEqualToString:(__bridge NSString *)kAXStaticTextRole]) {
        NSString *text = axString(element, kAXValueAttribute);
        if (text.length && texts.count < 20 && text.length <= 2000 && ![texts containsObject:text]) [texts addObject:text];
    } else if ([role isEqualToString:(__bridge NSString *)kAXButtonRole]) {
        NSString *title = axString(element, kAXTitleAttribute);
        if (title.length && title.length <= 200 && buttons.count < 8) [buttons addObject:@{@"title":title, @"element":(__bridge id)element}];
    }
    CFArrayRef children = NULL;
    if (AXUIElementCopyAttributeValue(element, kAXChildrenAttribute, (CFTypeRef *)&children) == kAXErrorSuccess && CFGetTypeID(children) == CFArrayGetTypeID()) {
        for (CFIndex i=0; i<CFArrayGetCount(children) && *budget>0; i++) dialogElements((AXUIElementRef)CFArrayGetValueAtIndex(children,i),texts,buttons,depth+1,budget);
    }
    if (children) CFRelease(children);
}

static BOOL axEnabled(AXUIElementRef element) {
    CFTypeRef value = NULL;
    AXUIElementCopyAttributeValue(element,kAXEnabledAttribute,&value);
    BOOL enabled = value == kCFBooleanTrue;
    if (value) CFRelease(value);
    return enabled;
}

int miya_desktop_dialog(unsigned int windowID, int pid, char **json) { @autoreleasepool {
    if (!AXIsProcessTrusted()) return 2;
    NSDictionary *info = windowInfo(windowID,pid); CGRect bounds;
    if (!info || !CGRectMakeWithDictionaryRepresentation((CFDictionaryRef)info[(id)kCGWindowBounds],&bounds)) return 4;
    AXUIElementRef window = copyWindowElement(bounds,pid);
    if (!window) return 4;
    NSMutableArray *texts=[NSMutableArray array], *buttons=[NSMutableArray array], *actions=[NSMutableArray array]; int budget=160;
    dialogElements(window,texts,buttons,0,&budget);
    CFTypeRef defaultButton=NULL, cancelButton=NULL;
    AXUIElementCopyAttributeValue(window,kAXDefaultButtonAttribute,&defaultButton);
    AXUIElementCopyAttributeValue(window,kAXCancelButtonAttribute,&cancelButton);
    for (NSDictionary *button in buttons) {
        AXUIElementRef element=(__bridge AXUIElementRef)button[@"element"];
        AXValueRef position=NULL, size=NULL; CGPoint p=CGPointZero; CGSize s=CGSizeZero;
        AXUIElementCopyAttributeValue(element,kAXPositionAttribute,(CFTypeRef *)&position);
        AXUIElementCopyAttributeValue(element,kAXSizeAttribute,(CFTypeRef *)&size);
        if (position) { AXValueGetValue(position,kAXValueCGPointType,&p); CFRelease(position); }
        if (size) { AXValueGetValue(size,kAXValueCGSizeType,&s); CFRelease(size); }
        [actions addObject:@{@"id":button[@"title"], @"label":button[@"title"], @"enabled":@(axEnabled(element)), @"default":@((BOOL)(defaultButton && CFEqual(defaultButton,element))), @"cancel":@((BOOL)(cancelButton && CFEqual(cancelButton,element))), @"x":@((p.x-bounds.origin.x)/bounds.size.width), @"y":@((p.y-bounds.origin.y)/bounds.size.height), @"width":@(s.width/bounds.size.width), @"height":@(s.height/bounds.size.height)}];
    }
    NSString *title = texts.count ? texts[0] : axString(window,kAXTitleAttribute) ?: @"";
    NSString *message = texts.count>1 ? [[texts subarrayWithRange:NSMakeRange(1,texts.count-1)] componentsJoinedByString:@"\n"] : @"";
    NSRange newline=[title rangeOfString:@"\n"];
    if (newline.location!=NSNotFound) {
        NSString *remainder=[title substringFromIndex:newline.location+1];
        title=[title substringToIndex:newline.location];
        message=message.length ? [remainder stringByAppendingFormat:@"\n%@",message] : remainder;
    }
    if (defaultButton) CFRelease(defaultButton); if (cancelButton) CFRelease(cancelButton); CFRelease(window);
    NSData *data=[NSJSONSerialization dataWithJSONObject:@{@"title":title,@"message":message,@"actions":actions} options:0 error:NULL];
    if (!data) return 9;
    *json=calloc(data.length+1,1); if (!*json) return 11; memcpy(*json,data.bytes,data.length); return 0;
} }

int miya_desktop_press(unsigned int windowID, int pid, const char *label) { @autoreleasepool {
    if (!AXIsProcessTrusted()) return 2;
    NSDictionary *info=windowInfo(windowID,pid); CGRect bounds;
    if (!info || !CGRectMakeWithDictionaryRepresentation((CFDictionaryRef)info[(id)kCGWindowBounds],&bounds)) return 4;
    AXUIElementRef window=copyWindowElement(bounds,pid); if (!window) return 4;
    NSMutableArray *texts=[NSMutableArray array], *buttons=[NSMutableArray array];int budget=160;
    dialogElements(window,texts,buttons,0,&budget);
    NSString *title=[NSString stringWithUTF8String:label]; AXUIElementRef match=NULL; int count=0;
    for (NSDictionary *button in buttons) if ([button[@"title"] isEqualToString:title]) {match=(__bridge AXUIElementRef)button[@"element"];count++;}
    int result=4;
    if (count==1 && axEnabled(match) && AXUIElementPerformAction(match,kAXPressAction)==kAXErrorSuccess) result=0;
    CFRelease(window); return result;
} }

static BOOL windowIsFront(CGWindowID windowID, pid_t pid, CGPoint point, BOOL pointer) {
    // The background service has no AppKit run loop; NSWorkspace's cached
    // frontmostApplication can be stale there. Query accessibility directly.
    AXUIElementRef system=AXUIElementCreateSystemWide(); CFTypeRef focused=NULL;pid_t front=0;
    AXUIElementCopyAttributeValue(system,kAXFocusedApplicationAttribute,&focused);
    if (focused) {AXUIElementGetPid((AXUIElementRef)focused,&front);CFRelease(focused);}
    CFRelease(system);
    if (front != pid) return NO;
    NSArray *windows = CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID));
    for (NSDictionary *info in windows) {
        if ([info[(id)kCGWindowAlpha] doubleValue] <= 0) continue;
        CGRect bounds;
        if (!CGRectMakeWithDictionaryRepresentation((CFDictionaryRef)info[(id)kCGWindowBounds], &bounds)) continue;
        if (pointer ? !CGRectContainsPoint(bounds, point) : [info[(id)kCGWindowOwnerPID] intValue] != pid) continue;
        return [info[(id)kCGWindowNumber] unsignedIntValue] == windowID && [info[(id)kCGWindowOwnerPID] intValue] == pid;
    }
    return NO;
}

int miya_desktop_window_input(unsigned int windowID, int pid, int kind, double x, double y, double tx, double ty, int dx, int dy, const unsigned short *text, int length, int key, int modifier) { @autoreleasepool {
    if (!AXIsProcessTrusted()) return 2;
    NSDictionary *info = windowInfo(windowID, pid);
    CGRect bounds;
    if (!info || !CGRectMakeWithDictionaryRepresentation((CFDictionaryRef)info[(id)kCGWindowBounds], &bounds)) return 4;
    CGPoint point = CGPointMake(bounds.origin.x+x*(bounds.size.width-1), bounds.origin.y+y*(bounds.size.height-1));
    // Hover motion never raises a window; skip it until the target is front.
    if (kind == 7 && !windowIsFront(windowID, pid, point, YES)) return 0;
    if (!windowIsFront(windowID, pid, point, kind <= 4)) {
        if (!focusWindow(info, bounds, pid)) return 4;
        for (int i = 0; i < 10 && !windowIsFront(windowID, pid, point, kind <= 4); i++) usleep(20000);
        if (!windowIsFront(windowID, pid, point, kind <= 4)) return 4;
    }
    return inputAtBounds(bounds, windowID, pid, kind, x, y, tx, ty, dx, dy, text, length, key, modifier);
} }
