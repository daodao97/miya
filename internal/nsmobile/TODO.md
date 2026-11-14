# Phone follow-ups

The table records completed phone ports and the remaining follow-ups.
Protocol state is retained where needed; receiving a frame never grants an
approval or starts one of these features implicitly.

| Feature | Hook for the next port |
| --- | --- |
| Remote desktop | Ported: retained live image/cursor, touch/trackpad/zoom, IME/password entry and Mac keys, connecting/error/ended/picker states, permission previews/actions, apps/displays, privacy, manual unlock, floating preview and exact-target resume. Session/protocol ownership lives in `phone/client`; frame validation and input geometry are shared with MyGo in `phone/desktopview`. Back/close revoke leases; background/disconnect clear frames, input and secrets before requesting a fresh lease on authoritative state. Native authentication and device validation remain below. |
| Multiple desktop management | Ported: add/switch/rename/remove/reload, secure registry migration and persistence, per-desktop drafts/attachments/conversation selection and caches/options. Switching ends remote control and remounts the destination under the new desktop identity. |

## Ported feature sheets

- Approvals, questions and MCP forms open from conversation request cards.
  `internal/nsrequests` supplies the same validation, choices, URL confirmation
  and remembered consent as desktop. Answers survive sheet dismissal and are
  keyed by request version. Replies require explicit taps, carry fingerprints,
  disable while pending, and handle acknowledgements, errors and disconnects.
  Remote desktop preview/control remains a separate port; takeover requests
  offer only the explicit cancel/continue actions.
- Delegations use `@` / `#` agent suggestions and More → Agent tasks. The
  shared NSGo panel provides list/detail, result/source navigation, cancellation,
  watches, recheck/stop-tracking, delivery resolution and stable mutation retries.
  Phone RPCs enforce connection ownership and timeouts.
- Automations have list, detail/run history and draft/create sheets from Home
  and More. Generated fields, schedule, checks, turn limit, enabled/carry-over/
  worktree options and run-now use the phone protocol. Uncertain creation
  retains its original ID and payload across reconnects. Webhook status and
  recent routing results are read-only, matching the legacy phone scope.
- Memory has project/user scope, refresh, revision checks and durable source
  conversation navigation. It remains read-only, matching `internal/mobile`.
- Sheets own their polling timers; closing stops reads. Capability checks,
  disconnected/error states and 44-point action targets apply throughout.

Shared framework-free code: memory kind/state/time labels moved from widgets
into `memory/presentation`; automation run lookup moved out of `mobile` into
`phone/protocol`; the collaboration user request moved from `appmodel` to
`delegation` with a compatibility alias. Safe automation cloning is shared by
desktop and both phone implementations in `automation.Clone`. The feature transport state lives in
`phone/client`, and the MyGo UI continues compiling.

`features_test.go` covers each sheet with nsgotest, including required answers,
MCP validation, memory revisions/scopes/source links, automation list/detail/run/
create retry identity, and delegation list/detail/mutation retry and disconnect.
Physical-device layout and interaction validation remain outstanding.

Validation (2026-11-14): `gofmt -l .`, localization check/generation, desktop
vet, `go build ./...`, the NSGo-tagged build, and iOS cross-vet passed. Phone,
shared-client/protocol, NSGo desktop/chat and automation tests pass. Full
integration has the pre-existing `TestDesktopPageRendersLiveConnectingAndEnded`
failure (missing Trackpad). The desktop workspace connection test now waits for
its asynchronous error result, eliminating a timing failure seen under load.

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

## Remote desktop platform limits and validation

- The pinned core exposes `View.OnTouch` with finger IDs, `Responder`, pinch,
  wheel and canvas APIs. Single-finger touch/drag/scroll, trackpad motion,
  hold-to-drag, two-finger scrolling/right-click and pinch are mapped in the
  framework-free helper. System cancellation drops pending clicks/drags.
- `Scope.OnKey` supplies named shortcuts and `Input` supplies native text/IME
  composition. There is no general surface key-down/key-up stream with raw
  modifier state. The port offers named Mac key/modifier buttons and native
  text entry; it does not claim arbitrary hardware-key forwarding.
- There is no NSGo biometric/device-authentication API. Manual Mac unlock is
  available. Saving and using a stored password require a fresh successful
  `PlatformHooks.DesktopAuthenticate` call plus secure storage; without a host
  adapter the UI explains the limitation and never bypasses authentication.
  Existing stored passwords can still be forgotten. Phone secure storage is
  unsupported by the pinned core unless the host supplies `SecretStore`.
- Frames update `SourceFunc` on one image node; unchanged encoded frames skip
  decoding and source replacement. Polling is serialized, input is ordered and
  bounded, and floating previews poll at most once per second. Target revisions
  and lease contexts reject stale workers; reconnect checks window ID+PID or
  display ID+UUID before resuming.
- nsgotest covers connecting/live/ended, retained image/editor IDs, Unicode
  text, touch scrolling/cancellation, pinch/wheel, modifier keys, float/restore,
  background cleanup and desktop management. Domain tests cover stale leases,
  exact-target resume, registry migration, separate drafts/options, authenticated
  unlock and multi-touch arbitration. Required build/vet/localization checks and
  the full requested integration suite pass with a worktree-local Go cache
  (the shared cache returned an invalid standard-library reference).
- The old MyGo `Trackpad` test was stale: the focused keyboard deliberately
  hides the floating capsule. The test now dismisses input with `Hide keyboard`
  before switching modes; no MyGo toolbar behavior change was needed.

Physical iOS/Android gesture delivery, hardware keyboards, secure-store/native
identity adapters, app packaging and real Mac capture remain device validation.
No dependency checkout was changed and no build was installed.
