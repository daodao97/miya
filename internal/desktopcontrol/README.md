# desktopcontrol

Local desktop capture and control through public macOS APIs and purego.
Desktop builds use `CGO_ENABLED=0`. macOS 14 or later supports captures;
other platforms return `ErrUnsupported`. Importing the plugin starts no
server and grants no remote access.

- `Displays`, `Windows`, `ApplicationInfo`: display/window identity and app metadata.
- `CaptureDisplay`, `CaptureWindow`: context-cancelable JPEG captures with a
  caller-chosen maximum dimension and quality. Call off the main UI thread.
  ScreenCaptureKit owns its completion blocks; canceled calls discard late
  results. purego v0.11 caches block trampolines by signature.
- `Post`, `PostToDisplay`, `PostToProcess`, `PostToWindow`: global-point input.
  Display identity is ID+UUID; window identity is ID+PID. Window-targeted input
  validates focus and refuses ambiguous AX matches. It never falls back to
  another window or a whole display. Unicode chunks preserve surrogate pairs.
- `ReadDialog`, `Press`: static labels and buttons only. Editable and secure
  text, document views and app navigation are excluded.
- `Locked`, `Wake`: console state and transient local-user activity.
- `RunPrivacy`: gamma blackening in a dedicated helper's main thread, restoring
  saved tables on return. The caller cancels its context on heartbeat loss or
  termination. Control+Option+Command+P restores locally. Displays that cannot
  apply and verify black gamma fail startup and restore tables already saved.

Use `plugins/permissionflow` to request Screen Recording and Accessibility.
This plugin reports missing permission without prompting. The application
owns user consent, remote sessions, input limits, coordinate normalization,
response budgets and helper process supervision.

`CGO_ENABLED=0 go test ./plugins/desktopcontrol` reads metadata and dialog
counts; it never sends input or changes physical gamma. Miya's opt-in native
fixture covers real input in its own disposable window.
