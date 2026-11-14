# Phone follow-ups

The table records completed phone ports and the remaining follow-ups.
Protocol state is retained where needed; receiving a frame never grants an
approval or starts one of these features implicitly.

| Feature | Hook for the next port |
| --- | --- |
| Remote desktop | Ported: retained live image/cursor, touch/trackpad/zoom, IME/password entry and Mac keys, connecting/error/ended/picker states, permission previews/actions, apps/displays, privacy, manual unlock, floating preview and exact-target resume. Session/protocol ownership lives in `phone/client`; frame validation and input geometry live in framework-free `phone/desktopview`. Back/close revoke leases; background/disconnect clear frames, input and secrets before requesting a fresh lease on authoritative state. Native authentication and device validation remain below. |
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
`phone/client`, and the old MyGo UI has been removed.

`features_test.go` covers each sheet with nsgotest, including required answers,
MCP validation, memory revisions/scopes/source links, automation list/detail/run/
create retry identity, and delegation list/detail/mutation retry and disconnect.
Physical-device layout and interaction validation remain outstanding.

Validation (2026-11-14): `gofmt -l .`, localization check/generation, desktop
vet, `go build ./...`, the NSGo-tagged build, and iOS cross-vet passed. Phone,
shared-client/protocol, NSGo desktop/chat and automation tests pass. At that stage, full
integration exposed a stale `TestDesktopPageRendersLiveConnectingAndEnded`
expectation (missing Trackpad), corrected by the later remote desktop validation
described below. The desktop workspace connection test now waits for
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
  notifications naming a known paired desktop switch to it before resolving
  the durable conversation. Unknown desktops ask for selection.
- Live Activities are opt-in on iOS for the currently viewed busy conversation.
  `widget.ActivityState` encodes the packaged NSGo widget schema for
  `StartActivityJSON`, `UpdateJSON` and `EndJSON`. Token rotations and reconnects
  register through the authenticated `activity` operation; empty tokens remove
  a subscription. The desktop service namespaces native IDs by device, validates
  the durable reference and publishes APNs updates/end events through
  `apns.ActivityManager`. Provider keys stay on the desktop. Background and a
  temporary disconnect preserve the activity; completion, navigation, desktop
  changes, disable and scope cleanup end it. Relaunch adopts an existing native
  activity using persisted public IDs/references and native token callbacks;
  tokens are never persisted. Widget URLs include the paired desktop identity.
  See [packaging and push validation](../../docs/nsgo-mobile-push.md).
- The conversation menu opens a refreshed sheet of verified conversation HTTP
  services. `phone/services` shares runtime identity/endpoint validation with
  MyGo. Opening passes the runtime token through `phone/proxy.NewPhoneWebProxy`
  for desktop revalidation and loads its authenticated loopback URL in an app
  WebView. Back, sheet dismissal, navigation, background, disconnect and connection
  replacement cancel outstanding work and release the proxy. Failed observation
  removes old entries. Project-wide candidate listings, manual ports and the
  background-task details UI remain outside this port.

## Native services and remaining platform gaps

- NSGo's `mobile.OnNotificationEvent` delivers APNs registration, token changes,
  receipts and queued cold-start opens. Registration requests native presentation
  mask zero; the adapter decodes `internal/apns.Payload` (`aps`, `mygoID`, `mygo`)
  and applies local foreground presentation/suppression. iOS background alerts
  remain system-owned. Local presses use the scope callback exactly once. Unread
  state frames own badges; delayed pushes cannot restore a cleared badge.
  Existing `PlatformHooks` are optional overrides, not required services.
- Android retains MyGo's paired `notice` delivery. NSGo's standalone APK has no
  Firebase SDK. An embedding host can forward FCM events via `NsgoView`; it must
  supply Firebase configuration, registration, permission handling and cold-launch
  intent forwarding. FCM tokens never go to the desktop APNs provider.
- Android uses one native foreground service with combined leases for paired
  notification delivery, microphone recording, remote desktop and active turns.
  It starts/updates while visible, retains an acknowledged lease in background,
  and stops after the final reason ends. Failed or late callbacks cannot claim
  background retention or revive a closed app. Recording requests microphone
  service type after microphone permission/capture succeeds; the phone views
  remote desktop frames, so that lease uses dataSync, not mediaProjection.
  Android OS limits still apply; task removal/process death ends NSGo work.

- Pairings, the multi-desktop registry, tunnel identity and optional desktop
  passwords now use NSGo's iOS Keychain / Android Keystore through `scopeSecrets`.
  The existing `Miya` service and base64 encoding are retained. The core migrates
  `nsgo-desktop-link`, then `desktop-link`, only when `desktops-v1` is missing;
  an empty registry or singleton tombstone means Forget. Registry writes precede
  singleton cleanup. Failed/locked reads block registry writes, including a
  pairing entered during launch, until Reload succeeds. No secrets enter
  preferences or ordinary files. On iOS, missing items can also migrate raw
  MyGo entries under `com.daodao.miya`, retiring each source only after a durable
  NSGo copy. This requires the same installed app's Keychain access group.
  Android's old MyGo Keystore file format is private to its bridge and cannot
  be imported through NSGo's secret API; it is not read as plaintext or silently
  replaced. A different app identity must pair again.
- Scan to connect uses `mobile.ScanQRCode` on iOS and Android. Successful codes
  pass the same validation as paste/manual entry; cancellation leaves pairing
  unchanged and errors keep the fallback available. Late results cannot replace
  a desktop selected in the meantime. Packaging must declare
  `apple.info.NSCameraUsageDescription` on iOS and `CAMERA` in
  `android.permissions`; embedded Android hosts also declare
  `ACCESS_NETWORK_STATE` (standalone NSGo apps already include it).
- Detailed network observation tracks availability and the sorted name/kind
  inventory. The initial report records state; coming online or changing
  interfaces wakes a waiting reconnect or gives a live tunnel a 20-second
  read deadline, matching MyGo without tearing down a roaming connection.
  Each dial first reads the current native path with cancellation/offline
  handling. On iOS, a bounded URLSession HEAD prepares cellular/local-network
  permissions and VPN/DNS64 routes before opening Tailcat sockets.
- Android startup registers Tailcat's interface getter before any connection.
  It uses NSGo's named default-path inventory, socket ioctls for index/MTU/IPv4,
  and bounded UDP route selection for real relay source addresses (including
  IPv6); connecting these UDP sockets sends no packet. Empty address lists stay
  nonnil so netmon never falls back to forbidden NETLINK_ROUTE. NSGo may report
  unnamed transports or several transports for one VPN link; these drive path
  identity but do not invent interfaces. The API describes the default path,
  not every underlying device interface. Real Android VPN/IPv6 transitions
  still require device verification.
- `nschat.View` now accepts a custom composer, remote image resolver and link
  callback. The phone uses native multiline input, with UTF-16 caret offsets
  converted for thread/file/slash suggestions. Rich inline reference chips,
  image pasting, and camera capture are not part of this core.

Physical-device validation, packaging/configuration for the NSGo mobile CLI,
camera permissions, accessible MyGo Keychain migration, Android tunnel routing
and network permission prompts still need device-level work. Cross-vet checks Go
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
  Existing stored passwords can still be forgotten. Native secure storage
  alone does not authenticate the person; `DesktopAuthenticate` remains required.
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
  the full requested integration suite pass using the shared Go cache.
- The old MyGo `Trackpad` test was stale: the focused keyboard deliberately
  hides the floating capsule. The test now dismisses input with `Hide keyboard`
  before switching modes; no MyGo toolbar behavior change was needed.

Physical iOS/Android gesture delivery, hardware keyboards, native
identity authentication, app packaging and real Mac capture remain device validation.
No dependency checkout was changed and no build was installed.

## Native-services validation (2026-11-14)

`native_test.go` exercises iOS/Android QR pairing, invalid/canceled/error results,
manual fallback, stale scan results, secure multi-desktop restore/rename/remove/
Forget, NSGo singleton and accessible MyGo iOS migration, binary/empty credential
round trips, missing/locked-store errors, network changes, offline/canceled
preflight and authoritative Android inventory. These use NSGo's headless native
service model, not `PlatformHooks`. Formatting, localization check/generation,
desktop vet, both builds, iOS cross-vet and the full requested phone/integration
test suite pass. Android cross-vet also passes. All commands use the shared Go
cache; no private cache, dependency changes, installation or device testing.

## Native push validation (2026-11-14)

The native defaults and authenticated activity protocol are implemented. All
requested checks pass: clean `gofmt -l .`, localization generation/check, targeted
nsmobile/phone/APNs vet, default and NSGo builds, iOS NSGo cross-vet and the full
nsmobile/phone/APNs/integration test suite. Controller activity tests also pass.
The new nsgotest seams cover native registration/token rotation, queued opens,
foreground suppression/badges, desktop and widget routing, activity reconnect/
relaunch/unregister and combined foreground-service leases, including failed
promotion, final background release and late callbacks. No private Go cache,
dependency checkout edits, installation, push or merge. Signed-device APNs,
ActivityKit/widget packaging and Android/FCM host validation remain as documented
in [push packaging](../../docs/nsgo-mobile-push.md).
