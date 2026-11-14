#!/usr/bin/env python3
"""Build immutable remote-update archives, or merge their manifest fragments."""
import argparse
import hashlib
import json
import pathlib
import re
import tarfile
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--source', type=pathlib.Path)
parser.add_argument('--target', choices=['darwin-arm64', 'darwin-amd64', 'linux-arm64', 'linux-amd64'])
parser.add_argument('--version')
parser.add_argument('--out', type=pathlib.Path, required=True)
parser.add_argument('--merge', type=pathlib.Path, help='directory containing manifest fragments')
args = parser.parse_args()
args.out.mkdir(parents=True, exist_ok=True)

if args.merge:
    fragments = [json.loads(p.read_text()) for p in sorted(args.merge.glob('miya-update-*.json'))]
    if not fragments:
        parser.error('no update manifest fragments')
    manifest = dict(fragments[0], packages={})
    for fragment in fragments:
        if fragment['version'] != manifest['version'] or fragment['protocol'] != manifest['protocol']:
            parser.error('release fragments disagree about version or service protocol')
        for target, package in fragment['packages'].items():
            if target in manifest['packages']:
                parser.error('duplicate target: ' + target)
            manifest['packages'][target] = package
    (args.out / 'miya-update.json').write_text(json.dumps(manifest, indent=2) + '\n')
else:
    if not args.source or not args.target or not args.version:
        parser.error('--source, --target and --version are required')
    if not re.fullmatch(r'\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?', args.version):
        parser.error('version must be a semantic version without a leading v')
    source = args.source.resolve(strict=True)
    expected = 'Miya.app' if args.target.startswith('darwin-') else 'miya'
    if source.name != expected:
        parser.error('source must be named ' + expected)
    name = f'miya-{args.version}-{args.target}.tar.gz'
    archive = args.out / name
    with tempfile.TemporaryDirectory() as work:
        identity = pathlib.Path(work) / 'release.json'
        identity.write_text(json.dumps({'version': args.version, 'target': args.target}))
        with tarfile.open(archive, 'w:gz', dereference=False) as tar:
            tar.add(source, arcname=expected)
            tar.add(identity, arcname='release.json')
    with archive.open('rb') as stream:
        digest = hashlib.file_digest(stream, 'sha256').hexdigest()
    protocol_source = (pathlib.Path(__file__).resolve().parent.parent / 'internal/service/protocol/svcproto.go').read_text()
    protocol = int(re.search(r'const Version = (\d+)', protocol_source)[1])
    manifest = {'version': args.version, 'protocol': protocol, 'packages': {args.target: {
        'url': f'https://github.com/daodao97/miya/releases/download/v{args.version}/{name}',
        'size': archive.stat().st_size, 'sha256': digest}}}
    (args.out / f'miya-update-{args.target}.json').write_text(json.dumps(manifest, indent=2) + '\n')
