#!/usr/bin/env python3
"""Verify product metadata in an actual NSGo .app, .ipa or .apk.

Uses only Python's standard library and codesign on Apple platforms. Android
checks parse binary XML directly, so CI needs no Android SDK for arm64.
"""
import argparse
import json
import plistlib
from pathlib import Path
import struct
import subprocess
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parent.parent
CONFIG = json.loads((ROOT / 'nsgo.yaml').read_text())


def require(ok, message):
    if not ok:
        raise SystemExit(message)


def entitlements(app):
    result = subprocess.run(['codesign', '-d', '--entitlements', '-', '--xml', str(app)], capture_output=True, check=True)
    return plistlib.loads(result.stdout) if result.stdout.strip() else {}


def apple(app, signed):
    mac = (app / 'Contents/Info.plist').exists()
    plist = app / ('Contents/Info.plist' if mac else 'Info.plist')
    info = plistlib.loads(plist.read_bytes())
    expected = dict(CONFIG['apple']['info'], CFBundleIdentifier=CONFIG['id'], CFBundleName=CONFIG['name'],
                    CFBundleDisplayName=CONFIG['name'], CFBundleShortVersionString=CONFIG['version'],
                    CFBundleVersion=str(CONFIG['build_number']))
    if not mac:
        expected.pop("LSMinimumSystemVersion", None)
    for key, value in expected.items():
        require(info.get(key) == value, f'{plist}: {key}: expected {value!r}, got {info.get(key)!r}')
    schemes = [s for t in info.get('CFBundleURLTypes', []) for s in t.get('CFBundleURLSchemes', [])]
    require(schemes == CONFIG['url_schemes'], 'URL scheme mismatch')
    binary = app / ('Contents/MacOS/' if mac else '') / info['CFBundleExecutable']
    marker = f"miya-release:{CONFIG['version']}:end".encode()
    if mac:
        require(marker in binary.read_bytes(), 'selfupdate.Build release marker missing')
    resources = app / 'Contents/Resources' if mac else app
    for locale in ('en', 'zh-Hans'):
        path = f'{locale}.lproj/InfoPlist.strings'
        require(any((directory / path).is_file() for directory in (resources, resources / 'languages')),
                f'missing {locale} localization')
    require(any(resources.glob('*.icns')) if mac else any(app.glob('*.png')), 'missing packaged icon')
    if mac or signed or 'iPhoneSimulator' in info.get('CFBundleSupportedPlatforms', []):
        subprocess.run(['codesign', '--verify', '--deep', '--strict', str(app)], check=True)
        ents = entitlements(app)
    else:
        ents = {}
    if mac:
        # No sandbox or separate helper existed in MyGo; all modes share this executable.
        require((app / 'Contents/MacOS/Miya').is_file(), 'same-binary service/MCP layout missing')
        require(not info.get('LSUIElement', False), 'Miya must retain its regular window/menu-bar behavior')
        for key, value in CONFIG.get('macos', {}).get('entitlements', {}).items():
            require(ents.get(key) == value, f'missing entitlement {key}')
    else:
        require(info.get('NSSupportsLiveActivities') is True, 'Live Activities missing')
        widget = app / 'PlugIns/Widgets.appex'
        wi = plistlib.loads((widget / 'Info.plist').read_bytes())
        require(wi['CFBundleIdentifier'] == CONFIG['id'] + '.widgets', 'widget identifier mismatch')
        require(wi['CFBundleVersion'] == str(CONFIG['build_number']), 'widget build number mismatch')
        if signed:
            require(ents.get('aps-environment') in ('development', 'production'), 'APNs entitlement missing')
            group = CONFIG['apple']['app_group']
            require(group in ents.get('com.apple.security.application-groups', []), 'app group missing')
            require(group in entitlements(widget).get('com.apple.security.application-groups', []), 'widget app group missing')
            app_id = ents.get('application-identifier', '')
            require(app_id.endswith('.' + CONFIG['id']), 'signed application identifier missing')
    return dict(platform='macos' if mac else 'ios', plist=info, entitlements=ents)


def binary_manifest(data):
    """Read Android's binary XML string pool and start-element attributes."""
    strings, elements = [], []
    offset = 8
    while offset < len(data):
        kind, header, size = struct.unpack_from('<HHI', data, offset)
        require(size > 0 and offset + size <= len(data), 'invalid binary manifest chunk')
        if kind == 1:
            count, _, flags, start, _ = struct.unpack_from('<5I', data, offset + 8)
            for i in range(count):
                pos = offset + start + struct.unpack_from('<I', data, offset + header + i * 4)[0]
                if flags & 0x100:
                    def length(pos):
                        n = data[pos]
                        return (((n & 127) << 8) | data[pos + 1], pos + 2) if n & 128 else (n, pos + 1)
                    _, pos = length(pos)
                    n, pos = length(pos)
                    strings.append(data[pos:pos + n].decode('utf-8'))
                else:
                    n = struct.unpack_from('<H', data, pos)[0]; pos += 2
                    if n & 0x8000:
                        n = ((n & 0x7fff) << 16) | struct.unpack_from('<H', data, pos)[0]; pos += 2
                    strings.append(data[pos:pos + n * 2].decode('utf-16le'))
        elif kind == 0x104:
            elements.append(('#text', {'text': strings[struct.unpack_from('<I', data, offset + 16)[0]]}))
        elif kind == 0x102:
            ext = offset + 16
            _, name, start, stride, count = struct.unpack_from('<IIHHH', data, ext)
            attrs = {}
            for i in range(count):
                pos = ext + start + stride * i
                _, key, raw, _, _, typ, value = struct.unpack_from('<IIIHBBI', data, pos)
                attrs[strings[key]] = strings[raw] if raw != 0xffffffff else (strings[value] if typ == 3 else value)
            elements.append((strings[name], attrs))
        offset += size
    return elements


def android(path):
    with zipfile.ZipFile(path) as archive:
        nodes = binary_manifest(archive.read('AndroidManifest.xml'))
        first = lambda name: next(attrs for tag, attrs in nodes if tag == name)
        manifest, sdk, app = first('manifest'), first('uses-sdk'), first('application')
        require(manifest['package'] == CONFIG['id'], 'Android package mismatch')
        require(manifest['versionName'] == CONFIG['version'], 'Android version mismatch')
        require(int(manifest['versionCode']) == CONFIG['build_number'], 'Android build number mismatch')
        require(int(sdk['minSdkVersion']) == 28, 'Android min SDK mismatch')
        require(int(sdk['targetSdkVersion']) == CONFIG['android']['target_sdk'], 'Android target SDK mismatch')
        require(app['label'] == CONFIG['name'] and 'icon' in app, 'Android label/icon missing')
        require(int(app['supportsRtl']) != 0, 'Android RTL support missing')
        require(int(first('activity')['launchMode']) == 2, 'singleTask link handling missing')
        require('networkSecurityConfig' in app, 'network-security resource missing')
        network = binary_manifest(archive.read('res/xml/network_security_config.xml'))
        require(next(a for tag, a in network if tag == 'base-config')['cleartextTrafficPermitted'] in (0, 'false'), 'general cleartext traffic became permitted')
        require(next(a for tag, a in network if tag == 'domain-config')['cleartextTrafficPermitted'] in (0xffffffff, 1, 'true'), 'loopback WebView access missing')
        domains = [a['text'] for tag, a in network if tag == '#text']
        require(set(domains) == {'127.0.0.1', 'localhost', '::1', '[::1]'}, 'cleartext exceptions changed')
        require(all(a.get('includeSubdomains', 0) in (0, 'false') for tag, a in network if tag == 'domain'), 'loopback subdomain policy changed')
        perms = {a['name']: a for tag, a in nodes if tag == 'uses-permission'}
        for p in CONFIG['android']['permissions'] + ['INTERNET', 'ACCESS_NETWORK_STATE']:
            require('android.permission.' + p in perms, f'Android permission missing: {p}')
        require([a['scheme'] for tag, a in nodes if tag == 'data' and 'scheme' in a] == CONFIG['url_schemes'], 'Android URL scheme missing')
        require(any(int(a.get('foregroundServiceType', 0)) & 1 for tag, a in nodes if tag == 'service'),
                'dataSync foreground service missing')
        require('android.permission.FOREGROUND_SERVICE_DATA_SYNC' in perms, 'dataSync foreground service permission missing')
        require('lib/arm64-v8a/libnsgo.so' in archive.namelist(), 'NSGo Android executable missing')
        return dict(platform='android', manifest=nodes, network_security=network)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('package', type=Path)
    parser.add_argument('--signed', action='store_true', help='require profile/APNs/App Group entitlements in an IPA')
    parser.add_argument('--json', action='store_true')
    args = parser.parse_args()
    if args.package.suffix == '.apk':
        result = android(args.package)
    elif args.package.suffix == '.ipa':
        with tempfile.TemporaryDirectory(dir=ROOT / 'build/.tmp') as temp:
            with zipfile.ZipFile(args.package) as archive:
                archive.extractall(temp)
            apps = list((Path(temp) / 'Payload').glob('*.app'))
            require(len(apps) == 1, 'IPA must contain one main app')
            result = apple(apps[0], args.signed)
    else:
        result = apple(args.package, args.signed)
    print(json.dumps(result, indent=2) if args.json else f'verified {result["platform"]}: {args.package}')


if __name__ == '__main__':
    main()
