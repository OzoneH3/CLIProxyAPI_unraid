#!/usr/bin/env python3
"""Check the supported dockerMan fields and wrapper deployment contract."""
import xml.etree.ElementTree as E
from pathlib import Path

r = E.parse('templates/cliproxyapi.xml').getroot()
assert r.tag == 'Container' and r.attrib == {'version': '2'}
for key, value in {'Name': 'CLIProxyAPI', 'Network': 'bridge', 'Shell': 'sh',
                   'Privileged': 'false', 'WebUI': 'http://[IP]:[PORT:8318]/',
                   'ExtraParams': '--restart=unless-stopped'}.items():
    assert r.findtext(key) == value, key
assert r.findtext('Repository') in ('cliproxyapi-unraid:local', 'ghcr.io/ozoneh3/cliproxyapi-unraid')
attrs = {'Name', 'Target', 'Default', 'Mode', 'Description', 'Type', 'Display', 'Required', 'Mask'}
configs = r.findall('Config')
assert len(configs) == 6
for c in configs:
    assert set(c.attrib) == attrs
    assert c.get('Name') and c.get('Description')
    assert c.get('Required') in ('true', 'false')
    assert c.get('Mask') in ('true', 'false')
ports = [c for c in configs if c.get('Type') == 'Port']
assert {c.get('Target') for c in ports} == {'8317', '8318'}
assert all(c.get('Mode') == 'tcp' and c.text == c.get('Target') for c in ports)
paths = [c for c in configs if c.get('Type') == 'Path']
assert len(paths) == 1
assert paths[0].get('Target') == '/data' and paths[0].get('Mode') == 'rw'
assert paths[0].text == '/mnt/user/appdata/cliproxyapi'
pw = next(c for c in configs if c.get('Target') == 'WEBUI_PASSWORD')
assert pw.get('Required') == pw.get('Mask') == 'true'
assert not pw.text and not pw.get('Default')
assert E.parse('ca_profile.xml').getroot().findtext('Profile').strip()
assert r.findtext('Description').strip()
assert Path('LICENSE').is_file()
print('Template and repository profile validated.')
