# NSGo phone core

`go build` selects `main_nsgo_mobile.go` on iOS and Android and
`main_nsgo.go` on desktop, using only platform constraints. NSGo is the only UI;
`main.go`, `main_android.go` and the legacy phone implementation are deleted.
Android network registration is owned by `nsmobile.setupNetwork`.

## Layout and ownership

- `internal/nsmobile`: application/lifecycle adapter, revision-backed retained
  views, home/projects/activity, conversation navigation, safe-area/keyboard
  insets, settings and model/effort/permission sheets, photos, references,
  quotas, handoff, cancellable voice input, notification/lifecycle adapters,
  verified conversation services and owned file/service WebViews. Touch rows
  have a 44-point minimum.
- `internal/phone/client`: UI-independent connection generations, reconnect
  backoff and path probes, protocol frame dispatch, durable thread identity,
  delta merging and cache subscriptions, pending send/upload/ack/echo state,
  queue actions, options, unread completion tracking, image previews, handoff,
  file search and file target validation. UI work is delivered through
  `Dispatch` and `Changed`; it imports neither UI framework nor `appmodel`.
- `internal/nschat`: the shared transcript, with optional phone composer,
  remote image-source resolution and owned link handling. Desktop defaults
  remain available.
- Existing `phone/cache`, `phone/proxy`, `phone/transfer`, chat composer/model/
  presentation, notification and agent catalog packages remain the shared
  sources for protocol types and framework-free helpers.

The client port lifts the original phone core into a reusable package without retaining the legacy phone implementation. Runtime fetching lives in
`phone/services`, and local notification IDs/targets live in `notification`;
To preserve the phone domain
boundary, `SameMessageOrder` moved from `appmodel` to `chat/model`, reference
title/context formatting moved to `chat/presentation`, and `DesktopRead` and
its durable unread key moved to `notification`. `appmodel` keeps compatibility
wrappers/aliases, for shared domain callers.

Pending messages are phone-owned projections. They never enter authoritative
history, disk caches or subscription fingerprints. Acknowledgements do not
clear a later draft; only matching live messages or queue entries retire a
projection. Connection generations reject stale responses, and cache/message
identity uses backend session IDs across numeric thread renumbering.

The retained adapter rebuilds destinations only when the destination key
changes. Incoming frames update bindings; they do not recreate the native
composer. Sheets own the model/effort/policy and settings presentations.
The outer surface keeps content above the keyboard; `FloatingConversation`
owns the conversation's home-indicator spacing.

## Deferred work

See [phone follow-ups](../internal/nsmobile/TODO.md) for all feature hooks and
platform gaps. Approvals, delegations, read-only memory and automation list/detail/create now
use phone sheets, with request cards and delegation content shared with desktop
in `internal/nsrequests`. Voice, push and web services are ported (see the TODO list).
Remote desktop and multi-desktop management are now ported;
`phone/client` owns their leases, targets, secure registry and composer sessions.
`phone/desktopview` owns framework-free frame validation and pointer mapping;
NSGo updates one retained image node per frame. Saved-password unlock requires
a native authentication hook. See the TODO for precise platform limits.
NSGo now supplies mobile Keychain/Keystore credentials, QR scanning and detailed
network paths. Pairing and the multi-desktop registry persist through the native
secure store; missing registries migrate the older core singleton keys. The
adapter retains the existing `Miya` base64 format and can copy accessible raw
MyGo iOS Keychain items under `com.daodao.miya` before retiring their sources.
Empty registries/tombstones prevent forgotten desktops returning; failed reads
block destructive writes until Reload. Credentials never fall back to
preferences or ordinary files. MyGo Android's private encrypted-file format is
not exposed by NSGo, and separate app identities do not share credentials.

Scan to connect validates the desktop QR through the same pairing action as
paste/manual entry. Cancel, invalid codes and camera errors preserve the current
desktop. Native callbacks cannot replace a subsequently selected desktop.
Declare `NSCameraUsageDescription` in `apple.info` and `CAMERA` in
`android.permissions` when packaging; Android embedding hosts also need
`ACCESS_NETWORK_STATE`.

Detailed paths drive the shared client's existing reconnect wake and live-tunnel
probe logic. Names and transport kinds form an order-independent path identity,
including unnamed Android transports. Connection workers read a fresh native
path before dialing; iOS also makes a cancellable URLSession HEAD to the public
relay to activate network permissions and VPN/DNS64 routes, as MyGo does.
Android registers a thread-safe Tailcat/netmon getter before loading credentials
or connecting. NSGo names are enriched with socket ioctls and real UDP relay
source addresses, including IPv6, without netlink enumeration or fabricated
addresses. Non-nil address lists prevent fallback to Android's forbidden
NETLINK_ROUTE. This is the default path's inventory, with no claim to enumerate
every interface beneath a VPN. Physical network/permission transitions need
device verification.

APNs registration, token changes, remote receipt/open events and Android foreground
services now use NSGo mobile APIs directly. The `activity` protocol operation
registers ActivityKit tokens with the desktop service's `apns.ActivityManager`;
provider credentials stay on the desktop. Background activities receive desktop
updates and completion/end pushes. Notification and widget opens select a known
paired desktop before resolving the durable conversation, including cold launch.
Android retains paired notices and uses acknowledged foreground-service leases
for notifications, voice recording, remote desktop and active turns. Optional
`PlatformHooks` remain compatible host overrides. See
[mobile push packaging](nsgo-mobile-push.md) for entitlements, widget schema,
foreground-service types and the Android host's FCM responsibilities.
Saved-password unlock still requires a fresh successful
`PlatformHooks.DesktopAuthenticate`; secure storage does not replace biometrics
or device authentication, which NSGo does not expose. Manual unlock and forgetting
stored passwords remain available without that hook.

## Validation

The phone has nsgotest interaction coverage for home/search, opening a
conversation, editor retention while streaming, new/existing sends, next-draft
ownership, queue/steer/stop/drain, model/effort/policy sheets, keyboard visibility
and handoff. Framework-free client tests cover delta identity, cache isolation,
failed uploads, repeated queued text, read versions, file boundaries and
network preparation cancellation. Integration boundaries include `nsmobile`
and the new client domain.

Required checks:

```sh
gofmt -l .
go run ./locales/gen -check
go generate ./locales
go vet ./internal/nsmobile/... ./internal/phone/... ./internal/apns/...
go build ./...
go build -o /dev/null .
GOOS=ios GOARCH=arm64 CGO_ENABLED=1 go vet ./internal/nsmobile/
go test ./internal/nsmobile/... ./internal/phone/... ./internal/apns/... ./tests/integration/...
```

2026-11-14: formatting, localization, both build modes, desktop vet and iOS
cross-vet passed. Phone, client, chat and entry-point tests passed. The remote
desktop port adds retained image/input/lifecycle tests. The full requested
integration suite passes, including the MyGo test corrected to hide the keyboard
before operating its deliberately hidden floating toolbar. No physical phone,
IPA/APK packaging or deployment is claimed by these checks.

2026-11-14 phone-services port: the required format/localization checks, desktop
and iOS vet, default and NSGo builds passed. The injected nsgotest voice,
notification registration/routing and verified web-service ownership tests pass,
as do the legacy MyGo phone, shared client and notification tests. The voice
WebSocket fixture now ignores unrelated ordinary HTTP probes of its ephemeral
port; these previously polluted its session-result channel. The full
integration run passes.
See [TODO.md](../internal/nsmobile/TODO.md) and [push packaging](nsgo-mobile-push.md)
for native service behavior and device validation still required.

2026-11-14 native-services port: the required checks above pass with the shared
Go cache, as does Android cross-vet. Headless native-service tests cover QR
pairing/fallback/cancellation/errors, secure persistence and migration, empty
Forget tombstones, binary secrets, network changes and canceled/offline preflight.
No private cache was created. Camera permission/capture, installed-app Keychain
access groups, Android routing/VPN/IPv6, iOS permission prompts, IPA/APK packaging
and biometric integration still need device validation.

2026-11-14 native push port: every required check above passes, including APNs
vet/tests and the full integration suite. Additional controller tests exercise
activity registration, token replacement, per-device namespaces, desktop reference
validation and widget update/end payloads. Native nsgotest seams cover launch
opens and desktop routing, token rotation, foreground suppression and badges,
activity reconnect/relaunch, and acknowledged Android service leases/failures.
All checks use the shared Go cache; no private cache or dependency edits were
created. Device signing, real APNs/ActivityKit delivery and Android host behavior
remain the packaging/device checks in [mobile push](nsgo-mobile-push.md).
