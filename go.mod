module miya

go 1.27.3

require (
	github.com/coder/websocket v1.8.15
	github.com/creack/pty v1.1.24
	github.com/ebitengine/purego v0.11.3
	github.com/klauspost/compress v1.20.0
	github.com/nsgo-dev/nsgo v0.3.5-0.20261127123952-772cfe0114df
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	github.com/tailscale/tailcat v0.7.0
	github.com/thesyncim/gopus v0.2.3
	golang.org/x/image v0.48.0
	golang.org/x/mod v0.43.0
	golang.org/x/sys v0.50.0
	golang.org/x/text v0.44.0
	tailscale.com v1.105.0-pre.0.20261013225543-3c0508fa283f
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/akutz/memconn v0.1.0 // indirect
	github.com/alecthomas/chroma/v2 v2.27.0 // indirect
	github.com/andybalholm/brotli v1.3.0 // indirect
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/creachadair/msync v0.10.1 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dblohm7/wingoes v0.0.0-20260526185140-fb298caac7ca // indirect
	github.com/dlclark/regexp2/v2 v2.11.0 // indirect
	github.com/evanw/esbuild v0.28.2 // indirect
	github.com/fxamacker/cbor/v2 v2.9.3 // indirect
	github.com/gaissmai/bart v0.29.0 // indirect
	github.com/gdamore/encoding v1.0.1 // indirect
	github.com/gdamore/tcell/v3 v3.6.0 // indirect
	github.com/go-json-experiment/json v0.0.0-20260820222146-c27c302e5fc3 // indirect
	github.com/go-sourcemap/sourcemap v2.1.4+incompatible // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/google/btree v1.1.3 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/google/licenseclassifier/v2 v2.0.0 // indirect
	github.com/hdevalence/ed25519consensus v0.2.0 // indirect
	github.com/jsimonetti/rtnetlink v1.4.2 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/makiuchi-d/gozxing v0.1.1 // indirect
	github.com/mdlayher/netlink v1.11.2 // indirect
	github.com/mdlayher/socket v0.7.0 // indirect
	github.com/mitchellh/go-ps v1.0.0 // indirect
	github.com/modelcontextprotocol/go-sdk v1.8.0 // indirect
	github.com/nsgo-dev/goja v0.1.0-nsgo.2.0.20261122032045-fee2b34506fc // indirect
	github.com/pires/go-proxyproto v0.15.0 // indirect
	github.com/pkg/sftp v1.13.11 // indirect
	github.com/safchain/ethtool v0.7.0 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/sergi/go-diff v1.4.0 // indirect
	github.com/tailscale/certstore v0.1.1-0.20260409135935-3638fb84b77d // indirect
	github.com/tailscale/gliderssh v0.3.4-0.20260716005906-1a0f895faf28 // indirect
	github.com/tailscale/go-winio v0.0.0-20231025203758-c4f33415bf55 // indirect
	github.com/tailscale/hujson v0.0.0-20260727124030-b80ff77dac4f // indirect
	github.com/tailscale/peercred v0.0.0-20250107143737-35a0c7bd7edc // indirect
	github.com/tailscale/web-client-prebuilt v0.0.0-20260917222731-e0ed2d0d0fea // indirect
	github.com/tailscale/wireguard-go v0.0.0-20260928213353-e61b6b78110b // indirect
	github.com/u-root/u-root v0.16.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	github.com/yuin/goldmark v1.8.7 // indirect
	go.yaml.in/yaml/v3 v3.0.6 // indirect
	go4.org/mem v0.0.0-20240501181205-ae6ca9944745 // indirect
	go4.org/netipx v0.0.0-20260823151212-3075585bcbeb // indirect
	golang.org/x/crypto v0.59.0 // indirect
	golang.org/x/exp v0.0.0-20260908205506-85c1c2202aba // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/oauth2 v0.37.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/term v0.48.0 // indirect
	golang.org/x/time v0.16.0 // indirect
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1 // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
	golang.zx2c4.com/wireguard/windows v1.0.1 // indirect
	gvisor.dev/gvisor v0.0.0-20260915211658-a6f909f08a72 // indirect
	rsc.io/qr v0.2.0 // indirect
)

tool github.com/nsgo-dev/nsgo/cmd/nsgo

replace github.com/nsgo-dev/nsgo => ../nsgo
