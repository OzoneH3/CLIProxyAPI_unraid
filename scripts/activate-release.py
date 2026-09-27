#!/usr/bin/env python3
"""Activate the public image only after an anonymous registry manifest check."""
import json
import urllib.parse
import urllib.request
from pathlib import Path

repo = 'ozoneh3/cliproxyapi-unraid'
query = urllib.parse.urlencode({'service': 'ghcr.io', 'scope': f'repository:{repo}:pull'})
with urllib.request.urlopen('https://ghcr.io/token?' + query, timeout=20) as response:
    token = json.load(response)['token']
req = urllib.request.Request(f'https://ghcr.io/v2/{repo}/manifests/latest', headers={
    'Authorization': 'Bearer ' + token,
    'Accept': 'application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json',
})
with urllib.request.urlopen(req, timeout=20) as response:
    manifest = json.load(response)
platforms = {m.get('platform', {}).get('architecture') for m in manifest.get('manifests', [])}
if not {'amd64', 'arm64'} <= platforms:
    raise SystemExit('Public multi-platform latest image not available; template unchanged.')
p = Path('templates/cliproxyapi.xml')
text = p.read_text().replace('cliproxyapi-unraid:local', f'ghcr.io/{repo}:latest')
text = text.replace('  <!-- Local build until public GHCR publication; see scripts/activate-release.py. -->\n', '')
p.write_text(text)
print('Public image verified. Review and commit the activated template before CA submission.')
