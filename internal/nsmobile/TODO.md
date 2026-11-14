# Phone follow-ups

The table records completed phone ports and the remaining follow-ups.
Protocol state is retained where needed; receiving a frame never grants an
approval or starts one of these features implicitly.

| Feature | Hook for the next port |
| --- | --- |
| Approvals and questions | Replace the desktop-review banner in `composer` with request cards; consume `Client.State.Approvals`. Add `responded` handling to `client.read`. |
| Remote desktop | Ported: retained live image/cursor, touch/trackpad/zoom, IME/password entry and Mac keys, connecting/error/ended/picker states, permission previews/actions, apps/displays, privacy, manual unlock, floating preview and exact-target resume. Session/protocol ownership lives in `phone/client`; frame validation and input geometry are shared with MyGo in `phone/desktopview`. Back/close revoke leases; background/disconnect clear frames, input and secrets before requesting a fresh lease on authoritative state. Native authentication and device validation remain below. |
| Multiple desktop management | Ported: add/switch/rename/remove/reload, secure registry migration and persistence, per-desktop drafts/attachments/conversation selection and caches/options. Switching ends remote control and remounts the destination under the new desktop identity. |
| Automations | Add list/detail/create destinations to `sheetContent`; add automation frame handlers to `client.read`. |
| Memory | Add a memory sheet; use state revisions to invalidate its data and add the memory frame handler. |
| Voice | Add a composer accessory beside photo selection; cancel recording on owner epoch, navigation, background, and disconnect. |
| Push | Add registration, badge and notification routing to the lifecycle adapter. Read-version acknowledgements already live in `phone/client`; no push registration is sent. Android foreground keep-running belongs here too. |
| Web services | Add the conversation-menu action and owned WebView/proxy lifecycle. Project files already use an authenticated phone file proxy. |
| Delegations | Add agent tags to the reference suggestions and collaboration frame handlers; thread/file references already work. |

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
