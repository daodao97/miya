#!/usr/bin/env python3
"""Adjust mygo.json for a CI packaging job. The checkout is disposable, so the
file is edited in place; local builds keep the committed configuration.

  scripts/ci-config.py [--version V] [--unsigned-ios]
                       [--ios-signing TEAM PROFILE_UUID]
                       [--notarize PROFILE KEYCHAIN]
"""
import argparse
import json
import re
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--version', help='app version, e.g. 1.2.3 (a leading v is dropped)')
parser.add_argument('--unsigned-ios', action='store_true', help='drop ios.developmentTeam so the device build is unsigned')
parser.add_argument('--ios-signing', nargs=2, metavar=('TEAM', 'PROFILE_UUID'), help='manual Apple Distribution signing')
parser.add_argument('--notarize', nargs=2, metavar=('PROFILE', 'KEYCHAIN'), help='notarytool keychain profile for the disk image')
args = parser.parse_args()

path = Path(__file__).resolve().parent.parent / 'mygo.json'
config = json.loads(path.read_text())

if args.version:
    version = args.version.removeprefix('v')
    if not re.fullmatch(r'[0-9]+(\.[0-9]+){0,2}([-+][A-Za-z0-9.-]+)?', version):
        raise SystemExit(f'invalid version {args.version!r}')
    config['version'] = version

ios = config.setdefault('ios', {})
if args.unsigned_ios:
    ios.pop('developmentTeam', None)
    ios.pop('signing', None)
if args.ios_signing:
    team, uuid = args.ios_signing
    if not re.fullmatch(r'[A-Z0-9]{10}', team):
        raise SystemExit('invalid team id')
    if not re.fullmatch(r'[A-Za-z0-9-]{1,100}', uuid):
        raise SystemExit('invalid profile UUID')
    ios['developmentTeam'] = team
    ios['signing'] = {
        'style': 'manual',
        'identity': 'Apple Distribution',
        'provisioningProfile': uuid,
        'exportCertificate': 'Apple Distribution',
        'exportProfiles': {config['identifier']: uuid},
    }

if args.notarize:
    profile, keychain = args.notarize
    config.setdefault('macos', {})['notarize'] = {'keychainProfile': profile, 'keychain': keychain}

path.write_text(json.dumps(config, indent=2, ensure_ascii=False) + '\n')
print(f"mygo.json: version {config['version']}")
