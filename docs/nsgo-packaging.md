# NSGo packaging

`nsgo.yaml` is the single product configuration. It uses JSON syntax (valid YAML)
so the Python CI helpers can edit it without installing PyYAML. `name`, `id`,
`version`, mobile `build_number`, icons, signing, APNs, Live Activities, App Group,
Android permissions and foreground service types use NSGo's schema. The `miya`
section holds the packaging properties that this NSGo revision cannot express.
Do not add a second product configuration to a script.

## Build commands and output

```sh
scripts/build-macos.sh                         # native architecture, ad hoc by default
scripts/build-macos.sh -target macos_universal -installers
scripts/build-ios.sh -target ios_simulator     # no device or provisioning needed
scripts/build-ios.sh                           # device-less IPA; unsigned without profiles
scripts/build-android.sh                       # arm64 APK; Go only, no SDK/NDK/Gradle
scripts/build-server.sh arm64                  # Linux, same selfupdate release marker
```

Builds go to `build/Miya-<version>-<target>/`; distribution files and checksums go
to `build/releases/`. NSGo's development files, including the persistent Android
debug key, live in `build/nsgo-cache/`, reached through an ignored `.nsgo` symlink.
Widget/icon products and Clang modules also stay under `build/nsgo-cache/`. Temporary compiler files use `build/.tmp/`; Go's normal shared `GOCACHE` is retained.
Keep the debug key if an existing debug APK must remain upgradeable. Release APKs
should use `sign.keystore`, `sign.key_alias`, `NSGO_KEYSTORE_PASSWORD` and
`NSGO_KEY_PASSWORD`; an old MyGo debug APK signed with another key will need an
uninstall or a build signed with its original key. No script installs by default.
NSGo's `-run -device SERIAL` explicitly installs and starts a selected mobile build.

All package builds retain `-tags nsgo`. `miya.ldflags` supplies
`-X miya/internal/selfupdate.Build=miya-release:<version>:end` to the linker.
The Linux server uses the same version from this file. The mobile entry does not
link `internal/selfupdate`, so there is no retained selfupdate marker in its binary.
The desktop `CFBundleVersion` stays equal to the marketing version; iOS and Android
use integer `build_number` (default 1). CI updates the iOS number from the run number.

For a stable local macOS signature set `MIYA_SIGNING_IDENTITY`, or put its single
line value in `build/nsgo-cache/macos-signing-identity`. These are local overrides;
shared signing settings belong in `sign`. Existing `.mygo/macos-signing-identity`
can be copied there manually; it is never read by the new builds. `mygo.json` and
the MyGo CLI tool declaration were removed. No `.mygo` files were tracked in this
worktree, and none are needed for packaging. MyGo's library dependency remains for
the old default entry and integration UI tests until the next migration step.

## Compatibility overlay

The bundle verification used read-only NSGo commit
`0820d6f731b3078823cf12a4930361c4672d32a7`. Its CLI lacks typed per-platform plist
values, URL scheme registration, per-platform icons and build numbers, custom iOS
Keychain groups, legacy Android permission SDK bounds, the `specialUse` foreground
service subtype, Android application/network-security policy, and universal macOS packaging. It also lacks extra application
build tags and linker flags.

`scripts/build-nsgo.sh` invokes `go tool nsgo build` with a Go source overlay made
by `scripts/packaging/nsgo-overlay.py`. It copies only the affected CLI sources to
`build/.nsgo-overlay/` and adds `nsgo-compat.go.txt` to that CLI package. It does not
write to the NSGo checkout/module cache, replace the runtime, or compile the old
MyGo host. Every source replacement is guarded: a changed upstream anchor aborts
before packaging rather than silently dropping a property. The hidden directory
also keeps generated tool sources out of `go build ./...`.

The bridge merges plist dictionaries before signing, adds the configured app tags
and ldflags, filters resources to the original localization directories, validates
requested Keychain groups against the signing profile, and extends NSGo's existing
binary Android manifest writer. `macos_universal` builds both architectures with
NSGo, merges every Mach-O (including native libraries) with `lipo`, then uses NSGo's
normal final signing, DMG and notarization flow. Single-architecture packages also
receive the normal final signature. The extra Android network-security resource extends its binary XML encoder with text chunks and adds a resource-table entry. This bridge currently supports APK, not AAB; asking for an Android store AAB fails explicitly. NSGo still supplies all native mobile host
code, widget Swift sources, icon conversion, APK v2 signing, IPA creation, and
notarytool submission/stapling.

Use the scripts, not a bare `go tool nsgo build`: the bare command does not know
Miya's extension fields. When NSGo gains these hooks, move the `miya` fields to its
schema and delete the matching guarded patches. A new NSGo pin must pass the local
package checks below, including universal libraries, before updating this bridge.

## Signing, platform permissions and extensions

macOS remains unsandboxed, with no service/login helper bundle. The same
`Contents/MacOS/Miya` executable runs the window, `-server`, `-mcp` and the privacy
helper mode. The launch agent remains `com.daodao.miya.service` with that binary
and `-server`; `internal/appdir.Isolated` suppresses login-item changes. There is
no `LSUIElement` setting: regular windows and the application's menu-bar item are
preserved. Network access does not need sandbox entitlements. Apple Events,
Accessibility and Screen Recording usage descriptions are now explicit in the
macOS plist; OS TCC authorization still comes from the existing native requests.
Neither a usage string nor an entitlement grants TCC permission automatically.

`sign.identity`/`-sign` selects a Developer ID for distribution;
`sign.notarize`/`-notarize` selects a notarytool keychain profile; `miya.notary_keychain` selects its keychain when it is not the default (CI sets both with `ci-config.py --notarize PROFILE KEYCHAIN`). CI can instead
use `NSGO_NOTARY_KEY`, `NSGO_NOTARY_KEY_ID` and `NSGO_NOTARY_ISSUER`. Without signing
credentials, macOS remains ad hoc. No notarization is claimed by a local unsigned
build. The configured `miya.macos.entitlements` map is included in the final
signature, including ad hoc builds; it is empty to preserve the MyGo baseline.

For iOS, provision `com.daodao.miya` with Push Notifications, Keychain groups and
`group.com.daodao.miya`, and `com.daodao.miya.widgets` with the same App Group.
Set `sign.profile` and `sign.widgets_profile` to both provisioning files and import
an appropriate signing identity. `miya.ios.development_team` keeps the original
team `XNDA84PBM3`; a profile's actual App Identifier Prefix takes precedence when
expanding `$(AppIdentifierPrefix)*`. `com.apple.token` remains requested for
existing credential access. If a profile does not grant a requested Keychain group,
the build fails instead of generating an IPA that cannot be installed. Distribution
signatures retain the profile's `aps-environment` (`development` or `production`).
Simulator ad hoc signatures have only the App Group; Apple developer/APNs/Keychain
entitlements require device provisioning. Camera, microphone, Face ID, local
network, ATS local-network allowance and English/Chinese strings are retained.

Live Activities are now explicitly enabled and packaged as `PlugIns/Widgets.appex`
plus `NSGoActivities.framework`; the committed MyGo config did not declare a widget
extension or App Group. NSGo needs the new shared group for this extension. Its
standard extension ID is `<app-id>.widgets`. Existing MyGo native browser Swift
(`resources/ios/MiyaFiles.swift`) is no longer packaged: it exports MyGo-only
symbols; the NSGo mobile entry uses NSGo's document/native services. This does not
remove that source while the old entry remains. Background modes are empty, as
before. Background Live Activity push delivery still needs the phone protocol
subscription work described in `internal/nsmobile/TODO.md`; packaging does not add
that protocol.

Android retains package `com.daodao.miya`, min SDK 29, target SDK 36, notification,
network, camera, microphone, biometrics, location, media/storage permissions and
their legacy max-SDK limits, `miya://`, and the `specialUse` foreground service with
its subtype explanation. The host activity changes to
`dev.oneo.nsgo.NsgoActivity`, and `libnsgo.so` is NSGo's Go executable, not a MyGo
c-shared library. Backups remain disabled, RTL and singleTask link delivery stay enabled, camera/microphone hardware stays optional, and package-visibility queries for camera/photo selection remain. The original network-security policy is retained as a binary XML resource: cleartext denied except for `127.0.0.1` and `localhost` WebViews. The foreground service must still be started by the application
and authorized by the OS; declaring it does not keep it running automatically.

FCM remains optional and is not bundled in the standalone APK (no Firebase SDK or
Google credentials were committed for the previous APK either). To enable FCM,
build an NSGo embedded Android host, add Firebase's service/configuration there,
and implement `NsgoView`'s token-registration and `remoteNotification(JSONObject)`
hooks from NSGo's `docs/architecture/phone-services.md`. The stock standalone APK
reports unsupported remote registration instead of pretending to receive FCM.

## CI publication prerequisite

The current `replace github.com/nsgo-dev/nsgo => ../nsgo-miya` cannot work on a
fresh runner. No published fork module/version is known yet, and this migration
does not invent one. Before enabling CI packages:

1. Publish the NSGo fork containing commit `0820d6f731b3078823cf12a4930361c4672d32a7`
   (or a descendant with all `miya-input`, `miya-webview`, `miya-mobile`, `miya-push`
   and platform-services changes) to a Go-fetchable repository.
2. Obtain its exact module path and immutable Go tag or pseudo-version. Set
   repository variables `NSGO_FORK_MODULE` and `NSGO_FORK_VERSION` to those values.
   Do not use `latest` or a branch. The original module imports stay
   `github.com/nsgo-dev/nsgo`; if the fork has another module path, the replacement
   must map that original module to the published fork/version.
3. `scripts/ci-pin-nsgo.sh` sets the require version and replaces/removes the local
   replacement in the disposable checkout, then downloads modules. Check that the
   guarded overlay applies. Alternatively commit that exact require/replace pin
   once publication is complete and simplify this step.

The Package workflow uses those inputs for every job. macOS builds a universal
DMG, iOS a signed IPA when **both** app and widget profile secrets exist (otherwise
an explicitly named unsigned compile artifact), Android an arm64 APK, and Linux
the two server architectures. Signed iOS needs `IOS_WIDGETS_PROFILE_BASE64` in
addition to the previous certificate/app profile secrets. This is an IPA signing
flow, not an Xcode archive/export flow; it does not produce dSYMs. App Store upload
and real-device validation remain a separate release step. Tags continue to
produce selfupdate manifests and a draft GitHub release.

## Verification

```sh
python3 scripts/verify-package.py build/Miya-0.1.0-macos_arm64/Miya.app
python3 scripts/verify-package.py build/Miya-0.1.0-ios_simulator/Miya.app
python3 scripts/verify-package.py build/releases/miya-0.1.0-ios.ipa --signed
python3 scripts/verify-package.py build/releases/miya-0.1.0-android-arm64.apk
TMPDIR="$PWD/build/.tmp" go build ./...
TMPDIR="$PWD/build/.tmp" GIT_CEILING_DIRECTORIES="$PWD/build/.tmp" go test ./tests/integration/...
```

`--json` prints inspected metadata; `--signed` additionally checks APNs, Keychain
and both App Group signatures. Package verification checks the actual binary
Android manifest, not just configuration text. The Git ceiling keeps a test's
non-repository temp directory below `build/.tmp/` from accidentally discovering the
parent Miya repository. Real window/service checks use the isolated procedure in
[install-and-upgrade.md](install-and-upgrade.md#独立验证不覆盖已安装应用). Delete large
verified bundles/archives when disk space is constrained; keep text evidence and
the Android signing key if it must be reused.
