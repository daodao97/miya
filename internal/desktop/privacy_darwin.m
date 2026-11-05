//go:build darwin && !ios && cgo

#import <ApplicationServices/ApplicationServices.h>
#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>
#include <stdlib.h>
#include <math.h>
#include <string.h>

// Own the gamma tables in this small, isolated process. No display capture,
// power setting, virtual display, DDC brightness or private API is involved.
typedef struct {
    CGDirectDisplayID id;
    uint32_t count;
    CGGammaValue *tables;
} MiyaPrivacyDisplay;
typedef struct {
    MiyaPrivacyDisplay displays[32];
    uint32_t count;
    EventHotKeyRef hotkey;
    EventHandlerRef handler;
    bool exitRequested;
} MiyaPrivacy;

static OSStatus privacyHotKey(EventHandlerCallRef next, EventRef event, void *context) {
    ((MiyaPrivacy *)context)->exitRequested = true;
    return noErr;
}

static bool privacyBlack(CGDirectDisplayID display) {
    CGGammaValue zero[256] = {0};
    if (CGSetDisplayTransferByTable(display, 256, zero, zero, zero) != kCGErrorSuccess) return false;
    CGGammaValue red[256], green[256], blue[256]; uint32_t count = 0;
    if (CGGetDisplayTransferByTable(display, 256, red, green, blue, &count) != kCGErrorSuccess || count == 0) return false;
    for (uint32_t i = 0; i < count; i++) {
        if (!isfinite(red[i]) || !isfinite(green[i]) || !isfinite(blue[i]) || fabsf(red[i]) > 0.001 || fabsf(green[i]) > 0.001 || fabsf(blue[i]) > 0.001) return false;
    }
    return true;
}

static bool privacySync(MiyaPrivacy *state) {
    CGDirectDisplayID ids[32]; uint32_t count = 0;
    if (CGGetOnlineDisplayList(32, ids, &count) != kCGErrorSuccess || count == 0 || count >= 32) return false;
    for (uint32_t i = 0; i < count; i++) {
        MiyaPrivacyDisplay *saved = NULL;
        for (uint32_t j = 0; j < state->count; j++) if (state->displays[j].id == ids[i]) saved = &state->displays[j];
        if (!saved) {
            if (state->count >= 32) return false;
            uint32_t capacity = CGDisplayGammaTableCapacity(ids[i]);
            if (!capacity || capacity > 65536) return false;
            CGGammaValue *tables = calloc(capacity * 3, sizeof(CGGammaValue));
            uint32_t samples = 0;
            if (!tables) return false;
            if (CGGetDisplayTransferByTable(ids[i], capacity, tables, tables+capacity, tables+capacity*2, &samples) != kCGErrorSuccess || !samples) { free(tables); return false; }
            // Keep each channel contiguous at the original capacity.
            saved = &state->displays[state->count++];
            saved->id = ids[i]; saved->count = capacity; saved->tables = tables;
            // Some displays return fewer entries than capacity; compact the
            // channels so restoration uses exactly the returned table length.
            memmove(tables+samples, tables+capacity, samples*sizeof(CGGammaValue));
            memmove(tables+samples*2, tables+capacity*2, samples*sizeof(CGGammaValue));
            saved->count = samples;
        }
        if (!privacyBlack(ids[i])) return false;
    }
    return true;
}

void miya_privacy_end(void *opaque) {
    MiyaPrivacy *state = opaque;
    if (!state) return;
    if (state->hotkey) UnregisterEventHotKey(state->hotkey);
    if (state->handler) RemoveEventHandler(state->handler);
    bool fallback = false;
    for (uint32_t i = 0; i < state->count; i++) {
        MiyaPrivacyDisplay *display = &state->displays[i];
        if (CGDisplayIsOnline(display->id) && CGSetDisplayTransferByTable(display->id, display->count, display->tables, display->tables+display->count, display->tables+display->count*2) != kCGErrorSuccess) fallback = true;
        free(display->tables);
    }
    if (fallback) CGDisplayRestoreColorSyncSettings();
    free(state);
}

void *miya_privacy_begin(void) {
    // Never open a window, add a second Dock icon or take keyboard focus.
    [[NSApplication sharedApplication] setActivationPolicy:NSApplicationActivationPolicyProhibited];
    MiyaPrivacy *state = calloc(1, sizeof(MiyaPrivacy));
    if (!state) return NULL;
    EventTypeSpec type = {kEventClassKeyboard, kEventHotKeyPressed};
    EventHotKeyID id = {'MiPr', 1};
    if (InstallApplicationEventHandler(privacyHotKey, 1, &type, state, &state->handler) != noErr ||
        RegisterEventHotKey(kVK_ANSI_P, controlKey | optionKey | cmdKey, id, GetApplicationEventTarget(), 0, &state->hotkey) != noErr || !privacySync(state)) {
        miya_privacy_end(state); return NULL;
    }
    return state;
}

int miya_privacy_tick(void *opaque) {
    MiyaPrivacy *state = opaque;
    // This helper has no NSApplication event loop. Drain and dispatch Carbon
    // hotkey events explicitly as well as servicing the main run loop.
    CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.001, true);
    EventTypeSpec type = {kEventClassKeyboard, kEventHotKeyPressed};
    EventRef event = NULL;
    while (ReceiveNextEvent(1, &type, 0, true, &event) == noErr) {
        SendEventToEventTarget(event, GetApplicationEventTarget());
        ReleaseEvent(event);
    }
    if (state->exitRequested) return 1;
    return privacySync(state) ? 0 : 2;
}
