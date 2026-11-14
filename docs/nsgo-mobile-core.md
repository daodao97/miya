# NSGo phone core

`go build -tags nsgo` selects `main_nsgo_mobile.go` on iOS and Android. The
untagged application still selects `main.go`; `internal/mobile` is unchanged.
Desktop NSGo builds continue to select `main_nsgo.go`. Android's MyGo JNI
startup in `main_android.go` is excluded under `nsgo`.

## Layout and ownership

- `internal/nsmobile`: application/lifecycle adapter, revision-backed retained
  views, home/projects/activity, conversation navigation, safe-area/keyboard
  insets, settings and model/effort/permission sheets, photos, references,
  quotas, handoff and project-file WebView. Touch rows have a 44-point minimum.
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

The client port lifts the original phone core into a reusable package while
leaving the legacy phone implementation intact. To preserve the phone domain
boundary, `SameMessageOrder` moved from `appmodel` to `chat/model`, reference
title/context formatting moved to `chat/presentation`, and `DesktopRead` and
its durable unread key moved to `notification`. `appmodel` keeps compatibility
wrappers/aliases, so existing MyGo callers use the same implementations.

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
platform gaps. Approvals, remote desktop, automations, memory, voice, push,
web services, delegations and multi-desktop management remain separate ports.
The current pinned NSGo has no mobile secure-store or network-path service and
no QR scanner. Connection-code pairing is session-only unless a host supplies
a secure-store adapter. No credentials fall back to ordinary files.

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
gofmt -l internal/nsmobile internal/phone/client
go tool mygo i18n check
go tool mygo generate
go vet ./internal/nsmobile/...
go build ./...
go build -tags nsgo -o /dev/null .
GOOS=ios GOARCH=arm64 CGO_ENABLED=1 go vet -tags nsgo ./internal/nsmobile/...
go test ./internal/nsmobile/... ./tests/integration/...
go test ./internal/phone/client/... ./internal/nschat/...
```

2026-11-14: formatting, localization, both build modes, desktop vet and iOS
cross-vet passed. Phone, client, chat and entry-point tests passed. The full
integration run failed only at the known existing failure,
`TestDesktopPageRendersLiveConnectingAndEnded` (missing Trackpad in the MyGo
remote desktop page). No physical phone, IPA/APK packaging or deployment is
claimed by these checks.
