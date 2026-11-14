# Phone follow-ups

The NSGo core deliberately does not expose controls for these pending ports.
Protocol state is retained where needed; receiving a frame never grants an
approval or starts one of these features implicitly.

| Feature | Hook for the next port |
| --- | --- |
| Approvals and questions | Replace the desktop-review banner in `composer` with request cards; consume `Client.State.Approvals`. Add `responded` handling to `client.read`. |
| Remote desktop | Add a destination to `sheetContent` and an action to `conversationMenu`; consume `desktop-open`, `desktop-preview`, and `desktop-action` frames. Own and end leases on Back/background/disconnect. |
| Multiple desktop management | Add the registry manager and per-desktop composer sessions. The core uses one active link, per-desktop disk caches/options, and reads the old active secure registry when available. |
| Automations | Add list/detail/create destinations to `sheetContent`; add automation frame handlers to `client.read`. |
| Memory | Add a memory sheet; use state revisions to invalidate its data and add the memory frame handler. |
| Delegations | Add agent tags to the reference suggestions and collaboration frame handlers; thread/file references already work. |

## Voice, notifications and web services

- Voice uses `voiceinput.Start` and NSGo's microphone, off the engine thread.
  Hold the microphone accessory for 350 ms; partial text replaces the captured
  UTF-16 selection in the draft. Release finishes recognition; the saved
  `phone-voice-auto-send` preference optionally sends the final text. Sliding
  60 points away or native touch cancellation discards capture and its preview.
  Navigation, composer epoch, link/generation changes, background and disconnect
  cancel it. A late session returned after a permission prompt is canceled.
  Denied microphone access has its own translated error. Recording is injected
  in nsgotest; actual microphone and cloud recognition require device testing.
- Notifications have a settings permission action, `push` registration and
  result handling, Android `notice` frames, foreground conversation suppression,
  durable session routing, local notification IDs and authoritative unread badges.
  The public device identifier is persisted separately for the NSGo adapter;
  token/provider secrets are never stored in preferences. Notification and voice
  preferences reuse the MyGo keys. Launch taps wait for the desktop state;
  notifications naming another desktop ask for desktop selection (the registry
  manager remains a separate port).
- Live Activities are opt-in for the currently viewed busy conversation. The
  adapter uses `widget.StartActivity`, `Update`, `End`, `OnPushToken` and thread
  URLs. They end on completion, navigation, background, disconnect or disable;
  local content is refreshed while active. ActivityKit tokens remain separate
  from alert-device tokens. Packaging must enable `live_activities.enabled` in
  `nsgo.yaml` and build the widget extension; otherwise settings reports that
  activities are unavailable. Background push updates need an authenticated
  desktop subscription operation, which the existing phone protocol lacks.
  `PlatformHooks.RegisterActivity` receives `apns.ActivityRegistration` rotations
  and empty-token unregisters for a host to connect to `apns.ActivityManager`;
  the provider must encode content with NSGo's `widget.ActivityState`. The default
  adapter does not invent a protocol operation or ship APNs signing keys.
- The conversation menu opens a refreshed sheet of verified conversation HTTP
  services. `phone/services` shares runtime identity/endpoint validation with
  MyGo. Opening passes the runtime token through `phone/proxy.NewPhoneWebProxy`
  for desktop revalidation and loads its authenticated loopback URL in an app
  WebView. Back, sheet dismissal, navigation, background, disconnect and connection
  replacement cancel outstanding work and release the proxy. Failed observation
  removes old entries. Project-wide candidate listings, manual ports and the
  background-task details UI remain outside this port.

## Platform gaps in the pinned NSGo checkout

- `core/notifications.go` implements local permission, show/close, badge and
  press APIs. The pinned checkout has **no general APNs alert-device registration
  or remote-notification receive/open API** (the `mobile` package only wraps
  tab bars, keyboard accessories, haptics and install APIs). ActivityKit's
  `widget.OnPushToken` is an activity token, not an alert-device token.
  `PlatformHooks.RegisterPush` and `ObserveRemoteNotifications` are explicit
  cancellable registration / observer hooks. The observer must queue launch
  taps and suppress native foreground banners so this adapter can decide
  presentation; payloads follow `internal/apns.Payload`. Without those hooks,
  iOS settings reports remote registration unavailable; local notifications and
  badges remain usable. No NSGo files were modified.
- NSGo exposes no Android foreground-service / keep-running API. Android can
  register for notices over the paired connection while the app runs. A native
  `PlatformHooks.KeepRunning` lease enables keeping that connection in background;
  without a lease the core retains its normal background disconnect behavior.
  This is not a claim of background delivery without a native service.


- `Scope.ReadSecret`, `StoreSecret`, and `DeleteSecret` return
  `ErrPlatformUnsupported` on iOS and Android. `scopeSecrets` preserves that
  error. Pairing works until process exit; the app shows a persistence warning
  and never stores connection secrets in a regular file. A native host may
  supply `PlatformHooks.SecretStore`. Secure values use base64 inside the
  NSGo credential adapter. The NSGo-specific pairing key is
  `nsgo-desktop-link`; an empty value records Forget without resurrecting an
  older MyGo registry entry.
- Android's legacy `main_android.go` is now excluded under `nsgo`; NSGo
  owns startup and logging. The old MyGo JNI interface inventory is also
  excluded. Android app UIDs cannot query `NETLINK_ROUTE`, and the pinned
  NSGo does not expose an equivalent native inventory. A host can provide
  `PlatformHooks.NetworkInterfaces` for netmon; actual Android tunnel
  connectivity needs device validation with that native service.
- There is no QR scanner API. Pair by pasting a desktop connection code.
- Network-path observation is macOS-only. Phones retain deadline/backoff,
  send-triggered retry and foreground reconnect. `PlatformHooks.PrepareNetwork`
  accepts a cancellable native permission/warm-up adapter; without one, the
  transport triggers the system permission normally. Detailed offline/denied
  diagnostics and interface-change probes await native support.
- `nschat.View` now accepts a custom composer, remote image resolver and link
  callback. The phone uses native multiline input, with UTF-16 caret offsets
  converted for thread/file/slash suggestions. Rich inline reference chips,
  image pasting, and camera capture are not part of this core.

Physical-device validation, packaging/configuration for the NSGo mobile CLI,
QR permissions, secure-store migration from the installed MyGo app, and
network permission prompts still need device-level work. Cross-vet checks Go
code; it does not build an IPA/APK or exercise platform services.
