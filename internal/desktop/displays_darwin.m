//go:build darwin && !ios && cgo

#import <Cocoa/Cocoa.h>
#import <ApplicationServices/ApplicationServices.h>
#include <string.h>

NSString *miya_display_uuid(CGDirectDisplayID display) {
    CFUUIDRef uuid = CGDisplayCreateUUIDFromDisplayID(display);
    if (!uuid) return nil;
    NSString *value = [(NSString *)CFUUIDCreateString(kCFAllocatorDefault, uuid) autorelease];
    CFRelease(uuid);
    return value;
}

BOOL miya_display_matches(CGDirectDisplayID display, NSString *uuid) {
    return display && CGDisplayIsActive(display) && uuid.length && [miya_display_uuid(display) isEqualToString:uuid];
}

int miya_desktop_displays(char **json) { @autoreleasepool {
    CGDirectDisplayID ids[32]; uint32_t count = 0;
    if (CGGetActiveDisplayList(32, ids, &count) != kCGErrorSuccess || count >= 32) return 13;
    NSMutableDictionary *names = [NSMutableDictionary dictionary];
    // NSScreen requires an application object. Do not run or activate it; the
    // service remains a background process and only reads screen metadata.
    if (NSApp == nil) [[NSApplication sharedApplication] setActivationPolicy:NSApplicationActivationPolicyProhibited];
    for (NSScreen *screen in NSScreen.screens) {
        NSNumber *id = screen.deviceDescription[@"NSScreenNumber"];
        if (id && screen.localizedName.length) names[id] = screen.localizedName;
    }
    NSMutableArray *displays = [NSMutableArray array];
    for (uint32_t i = 0; i < count; i++) {
        CGDirectDisplayID id = ids[i];
        NSString *uuid = miya_display_uuid(id);
        if (!uuid) continue;
        CGRect bounds = CGDisplayBounds(id);
        [displays addObject:@{@"id": @(id), @"uuid": uuid, @"name": names[@(id)] ?: @"", @"main": @((BOOL)(id == CGMainDisplayID())), @"width": @(CGDisplayPixelsWide(id)), @"height": @(CGDisplayPixelsHigh(id)), @"x": @(bounds.origin.x), @"y": @(bounds.origin.y)}];
    }
    [displays sortUsingComparator:^NSComparisonResult(NSDictionary *a, NSDictionary *b) {
        if ([a[@"main"] boolValue] != [b[@"main"] boolValue]) return [a[@"main"] boolValue] ? NSOrderedAscending : NSOrderedDescending;
        NSComparisonResult order = [a[@"x"] compare:b[@"x"]];
        if (order == NSOrderedSame) order = [a[@"y"] compare:b[@"y"]];
        if (order == NSOrderedSame) order = [a[@"uuid"] compare:b[@"uuid"]];
        return order;
    }];
    NSData *data = [NSJSONSerialization dataWithJSONObject:displays options:0 error:nil];
    if (!data) return 13;
    *json = strndup(data.bytes, data.length);
    return *json ? 0 : 11;
}}
