# NSGo packaging

`nsgo.yaml` uses the NSGo project schema in JSON-compatible YAML. It sets the app
identifier, marketing version, integer build number, icon, URL schemes, Apple
permission strings, iOS App Group and Live Activities, and Android permissions.

Local development resolves `github.com/nsgo-dev/nsgo` to the adjacent `../nsgo`
checkout. CI removes this replacement with `scripts/ci-pin-nsgo.sh` and resolves the
pinned upstream version in `go.mod`. The packaging scripts call `go tool nsgo build`
and place build output and the project cache under `build/`.

```bash
scripts/build-macos.sh
scripts/package-macos.sh
scripts/build-ios.sh -target ios_simulator
scripts/build-ios.sh
scripts/build-android.sh
scripts/build-server.sh
```

The macOS scripts produce an app for the host architecture or a universal DMG.
Set `MIYA_SIGNING_IDENTITY` to select a local Developer ID identity. A release build
with `-release` can use a notarytool profile via `-notarize PROFILE` or an API key via
`NSGO_NOTARY_KEY`, `NSGO_NOTARY_KEY_ID`, and `NSGO_NOTARY_ISSUER`.

Device distribution requires the app and widget provisioning profiles under
`sign.profile` and `sign.widgets_profile`, an imported signing identity, and the
`group.com.daodao.miya` entitlement on both profiles. CI gets the iOS build number
from the workflow run number. The workflow also supports TestFlight distribution
from `main` with both profiles and the App Store Connect credentials.

The Android build uses the native dataSync foreground service and restricts cleartext
access to loopback for local previews. To reuse a release signing key, configure
`sign.keystore`, `sign.key_alias`, `NSGO_KEYSTORE_PASSWORD`, and `NSGO_KEY_PASSWORD`.

Use `python3 scripts/verify-package.py PATH` to inspect the resulting app, IPA, or
APK. Pass `--signed` for a distribution IPA to also check its profile entitlements.
