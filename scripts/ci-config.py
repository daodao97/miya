#!/usr/bin/env python3
"""Edit the JSON-compatible YAML in nsgo.yaml in disposable CI checkouts.

No PyYAML dependency is needed. Product properties live only in nsgo.yaml;
credentials remain in environment variables and ignored profile/key files.
"""
import argparse
import json
import re
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--version')
parser.add_argument('--build-number', type=int)
parser.add_argument('--unsigned-ios', action='store_true')
parser.add_argument('--ios-signing', nargs=3, metavar=('TEAM', 'APP_PROFILE', 'WIDGETS_PROFILE'))
parser.add_argument('--notarize', nargs=2, metavar=('PROFILE', 'KEYCHAIN'))
args = parser.parse_args()
path = Path(__file__).resolve().parent.parent / 'nsgo.yaml'
config = json.loads(path.read_text())
if args.version:
    version = args.version.removeprefix('v')
    if not re.fullmatch(r'[0-9]+(\.[0-9]+){0,2}([-+][A-Za-z0-9.-]+)?', version):
        raise SystemExit(f'invalid version {args.version!r}')
    config['version'] = version
if args.build_number is not None:
    if not 0 < args.build_number <= 2100000000:
        raise SystemExit('build number must be 1..2100000000')
    config['build_number'] = args.build_number
sign = config.setdefault('sign', {})
if args.unsigned_ios:
    for key in ('identity', 'profile', 'widgets_profile'):
        sign.pop(key, None)
if args.ios_signing:
    team, app, widgets = args.ios_signing
    if not re.fullmatch(r'[A-Z0-9]{10}', team):
        raise SystemExit('invalid team id')
    for profile in (app, widgets):
        if not Path(profile).is_file():
            raise SystemExit(f'missing provisioning profile: {profile}')
    config['miya']['ios']['development_team'] = team
    sign.update(profile=app, widgets_profile=widgets)
if args.notarize:
    sign['notarize'], config['miya']['notary_keychain'] = args.notarize
path.write_text(json.dumps(config, indent=2, ensure_ascii=False) + '\n')
print(f"nsgo.yaml: version {config['version']}, mobile build {config['build_number']}")
