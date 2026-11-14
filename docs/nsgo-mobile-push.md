# NSGo phone push and activity packaging

The phone uses NSGo's mobile APIs at `../nsgo-miya` revision `0820d6f7`.
`PlatformHooks.RegisterPush`, `ObserveRemoteNotifications`, `KeepRunning` and
`RegisterActivity` are optional host overrides. Ordinary standalone iOS builds
need no host push adapter. Native callbacks post onto the phone scope; headless
tests inject the same asynchronous boundaries through per-app `phoneServices`.

## iOS

Merge these options into the mobile app's `nsgo.yaml`:

```yaml
ios:
  remote_notifications: true
  # Alert pushes require no background mode. Add remote-notification only
  # when also opting into discretionary silent/background push delivery.
  # background_modes: [remote-notification]
apple:
  app_group: group.com.daodao.miya
live_activities:
  enabled: true
  # frequent_updates: true  # optional, subject to the user's/system's budget
```

Keep the application bundle ID `com.daodao.miya`, matching the desktop APNs
provider and the installed MyGo identity when migrating. Register the `miya` URL
scheme in the application's Info.plist/`apple.info.CFBundleURLTypes`, for both
thread URLs and `miya://notification` widget links. Live Activities alone cause
NSGo to build/embed the widget extension; no home-screen `widgets` entry is
required. If adding such entries, keep their configured kinds separate from
`miya-conversation`. The app and extension must use the same registered App Group
and correctly signed provisioning profiles for their respective bundle IDs.
NSGo emits `NSGoAppGroup` and `NSSupportsLiveActivities` and merges the app-group
entitlement. Do not substitute a custom Swift ActivityAttributes schema: local
and desktop content is encoded by `widget.ActivityState`, for NSGo's generated
`NSGoActivityAttributes` and widget extension.

Enable Push Notifications on the App ID and sign the app with a profile granting
`aps-environment` (`development` or `production`). NSGo `dev` requests development;
profile-signed builds retain their profile's environment. The extension needs
its own app-group entitlement, not the app's APNs entitlement. A simulator's ad
hoc signature does not establish real APNs device registration. Match the desktop
provider's sandbox/production environment to the signed device build.

The settings action requests notification permission. Each foreground launch/
return checks permission and registers again; token events, including rotations,
use the existing `push` operation and `notification.Registration`. Native remote
foreground presentation is zero so the phone can suppress the open conversation
and show other foreground notices locally. Native opens decode `mygoID`/`mygo`
from `internal/apns.Payload`, with queued launch opens waiting for pairing and
state. Background APNs alerts are presented by iOS, avoiding duplicate local
alerts. Authoritative unread frames own badges; delayed receipt does not restore
old counts. Android host notices may also use `notification.LiveMessage` JSON.

Live Activities start only on visible iOS for the viewed busy conversation when
enabled. They remain subscribed through background/disconnection, refresh locally
while visible, and end on completion, navigation, desktop change or disable.
Public native activity ID, reference and desktop ID are persisted to adopt an
existing activity after relaunch. Tokens remain in memory, arrive from native
callbacks, and register again after connection replacement. Early push-to-start
tokens (`Activity == nil`) are ignored: the phone starts activities itself.

The authenticated paired phone protocol accepts `op: "activity"` with an
`activity` object containing `deviceId`, native `id`, `token`, durable thread
`reference` and optional phone-registry `desktop` ID. Empty token unregisters.
Desktop replies use frame type `activity` with optional `error`. Device IDs
namespace native activity IDs; references must belong to that desktop and resolve
to a known conversation. Subscriptions live in memory for at most eight hours,
independent of a socket. The desktop service checks state every five seconds;
`apns.ActivityManager` deduplicates and refreshes unchanged content each minute,
ends completed/missing conversations, and removes permanently invalid tokens.
Disabling push removes that device's activities too. A service restart requires
phone reconnect/registration. Existing alert-push operations are unchanged.

The desktop alone opens its existing APNs provider settings and signing key.
Activity pushes use the activity token, `apns-push-type: liveactivity` and topic
`com.daodao.miya.push-type.liveactivity`; app alert tokens are a separate channel.
No APNs provider key is packaged or persisted on the phone.

## Android

```yaml
android:
  foreground_service_types: [dataSync, microphone]
  permissions: [RECORD_AUDIO]
```

NSGo adds `FOREGROUND_SERVICE`, the corresponding type permissions and
`POST_NOTIFICATIONS` and declares its unexported foreground service. Embedded
hosts must declare the equivalent manifest service/permissions themselves. The
microphone operation obtains its runtime permission through NSGo microphone
capture. Notification permission is requested through the phone settings flow.
Remote desktop receives frames from the Mac; it does not capture the phone's
screen and needs no mediaProjection service type.

One native service combines the current reasons: paired notification delivery,
active turns and remote desktop use dataSync; active voice recording adds
microphone. The phone retains its connection in background only after promotion
succeeds. Type changes are serialized; releasing one reason preserves the others,
and releasing the final reason stops the service. Starts/type updates are made
while visible. Voice input still follows the existing cancellation on background.
Service failure, unsupported packaging, cleanup and late completions do not grant
a background lease. Android's type prerequisites, background-start restrictions
and dataSync time limits still apply. NSGo stops on task removal/view destruction;
this does not create a restartable background Go process. Raw JSON Live Activities
are iOS-only; the phone reports those unavailable on Android.

The standalone APK includes no FCM SDK/configuration. MyGo-compatible Android
push uses `Live: true` registrations and `notice` frames over the paired socket.
For FCM an embedding host must supply Firebase, its service, permission flow,
`NsgoView.setRemoteNotificationRegistration`, token rotations and
`NsgoView.remoteNotification` receipt/open events. It must queue events while no
view exists and forward cold-launch extras and `onNewIntent`. FCM tokens are
never sent to the APNs provider. See NSGo's
[`phone-services.md`](../../nsgo-miya/docs/architecture/phone-services.md) for the
host contract; an unmodified standalone APK does not gain FCM delivery.

## Validation

`push_native_test.go` covers native registration/rotations/errors, silent
presentation, durable opens, cold launch and desktop selection, unread badges,
activity token rotation/reconnect/relaunch/unregister, widget URL routing, combined
foreground leases, promotion failure and late completion. Controller tests cover
the paired activity operation, device namespaces, reference validation and actual
manager update/end payloads using an injected APNs sender.

Physical-device checks remain necessary: signed APNs registration and rotation,
permission denial/revocation, background/terminated notification opens, widget
extension signing, ActivityKit lock-screen/Dynamic Island updates and completion
pushes with the phone suspended, Android microphone/service permissions and OS
limits, and embedding-host FCM cold starts. Go tests/cross-vet do not build or
install an IPA/APK and cannot establish those native delivery guarantees.
