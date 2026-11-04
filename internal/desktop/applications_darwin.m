//go:build darwin && !ios && cgo

#import <Cocoa/Cocoa.h>
#include <stdlib.h>
#include <string.h>

int miya_desktop_application(const char *path, int icon, char **json) { @autoreleasepool {
    NSString *bundlePath = [NSString stringWithUTF8String:path];
    NSBundle *bundle = [NSBundle bundleWithPath:bundlePath];
    if (!bundle) return 1;
    NSString *name = [bundle objectForInfoDictionaryKey:@"CFBundleDisplayName"];
    if (![name isKindOfClass:[NSString class]] || !name.length) name = [bundle objectForInfoDictionaryKey:@"CFBundleName"];
    if (![name isKindOfClass:[NSString class]] || !name.length) name = bundlePath.lastPathComponent.stringByDeletingPathExtension;
    NSMutableDictionary *result = [NSMutableDictionary dictionaryWithDictionary:@{@"name": name, @"bundleId": bundle.bundleIdentifier ?: @""}];
    if (icon) {
        NSImage *image = [[NSWorkspace sharedWorkspace] iconForFile:bundlePath];
        NSBitmapImageRep *bitmap = [[[NSBitmapImageRep alloc] initWithBitmapDataPlanes:NULL pixelsWide:48 pixelsHigh:48 bitsPerSample:8 samplesPerPixel:4 hasAlpha:YES isPlanar:NO colorSpaceName:NSDeviceRGBColorSpace bytesPerRow:0 bitsPerPixel:0] autorelease];
        if (image && bitmap) {
            [NSGraphicsContext saveGraphicsState];
            [NSGraphicsContext setCurrentContext:[NSGraphicsContext graphicsContextWithBitmapImageRep:bitmap]];
            [image drawInRect:NSMakeRect(0, 0, 48, 48) fromRect:NSZeroRect operation:NSCompositingOperationCopy fraction:1.0];
            [NSGraphicsContext restoreGraphicsState];
            NSData *png = [bitmap representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
            if (png.length && png.length <= 16384) result[@"icon"] = [png base64EncodedStringWithOptions:0];
        }
    }
    NSData *data = [NSJSONSerialization dataWithJSONObject:result options:0 error:nil];
    if (!data) return 1;
    *json = strndup(data.bytes, data.length);
    return *json ? 0 : 1;
}}
