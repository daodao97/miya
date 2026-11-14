# Phone follow-ups

The NSGo core deliberately does not expose controls for these pending ports.
Protocol state is retained where needed; receiving a frame never grants an
approval or starts one of these features implicitly.

| Feature | Hook for the next port |
| --- | --- |
| Remote desktop | Add a destination to `sheetContent` and an action to `conversationMenu`; consume `desktop-open`, `desktop-preview`, and `desktop-action` frames. Own and end leases on Back/background/disconnect. |
| Multiple desktop management | Add the registry manager and per-desktop composer sessions. The core uses one active link, per-desktop disk caches/options, and reads the old active secure registry when available. |
| Voice | Add a composer accessory beside photo selection; cancel recording on owner epoch, navigation, background, and disconnect. |
| Push | Add registration, badge and notification routing to the lifecycle adapter. Read-version acknowledgements already live in `phone/client`; no push registration is sent. Android foreground keep-running belongs here too. |
| Web services | Add the conversation-menu action and owned WebView/proxy lifecycle. Project files already use an authenticated phone file proxy. |

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

## Platform gaps in the pinned NSGo checkout

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
