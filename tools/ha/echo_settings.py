#!/usr/bin/env python3
"""Make a settings dashboard for your TECHO5 devices in Home Assistant.

    python3 tools/ha/echo_settings.py --url http://homeassistant.local:8123

It signs in to Home Assistant with a long-lived access token (the HA_TOKEN environment variable, or a
prompt; the token is never written to disk), finds every TECHO5 device in the device registry, reads
their entities, and writes two files:

  echo_settings_dashboard.yaml   paste into a dashboard (Raw configuration editor)
  echo_settings_package.yaml     copy into /config/packages (helpers and scripts)

Nothing is changed in Home Assistant unless you pass --push, which saves the dashboard (only) under
the address /echo-settings. Python 3 standard library only.
"""
import argparse
import base64
import getpass
import hashlib
import json
import os
import re
import secrets
import socket
import ssl
import struct
import sys
import urllib.parse

DASHBOARD_PATH = 'echo-settings'


class Fail(Exception):
    """A reason to stop, said plainly."""


# ---------------------------------------------------------------------------------------------------
# Settings, by section. (key, label, copy): copy=True means the setting is copied to the other devices
# when "All devices" is on. The key is the entity's unique id with the device's MAC address taken off.
# A device that lacks an entity (screen settings on a Dot) simply does not show that row.
SECTIONS = [
    ('Voice', 'mdi:account-voice', 'Main wake word and how the assistant listens and replies.', [
        ('wake_word', 'Wake word', True),
        ('pipeline', 'Assistant', True),
        ('4/number/Wake word sensitivity', 'Wake word sensitivity', True),
        ('vad_sensitivity', 'Finished speaking detection', True),
        ('4/number/Follow-up time', 'Follow-up time', True),
        ('4/number/Follow-ups in a row', 'Follow-ups in a row', True),
        ('4/number/Max listening time', 'Max listening time', True),
        ('4/number/Max thinking time', 'Max thinking time', True),
        ('4/number/Reply buffer', 'Reply buffer', True),
        ('4/select/Reply delivery', 'Reply delivery', True),
        ('4/number/Recordings kept', 'Recordings kept', True),
    ]),
    ('Voice sounds & effects', 'mdi:bell-ring-outline', 'Tones and ring/screen effects for wake word 1.', [
        ('4/select/Wake word tone', 'Wake word tone', True),
        ('4/select/Follow-up tone', 'Follow-up tone', True),
        ('4/select/Wake word effect', 'Wake word effect', True),
        ('4/select/Thinking effect', 'Thinking effect', True),
        ('4/select/Replying effect', 'Replying effect', True),
    ]),
    ('Second wake word', 'mdi:numeric-2-circle-outline', 'Optional second wake word with its own assistant.', [
        ('wake_word_2', 'Wake word 2', True),
        ('pipeline_2', 'Assistant 2', True),
        ('5/number/Wake word sensitivity', 'Wake word sensitivity', True),
        ('5/number/Follow-up time', 'Follow-up time', True),
        ('5/number/Follow-ups in a row', 'Follow-ups in a row', True),
        ('5/number/Max listening time', 'Max listening time', True),
        ('5/number/Max thinking time', 'Max thinking time', True),
        ('5/number/Reply buffer', 'Reply buffer', True),
        ('5/select/Reply delivery', 'Reply delivery', True),
        ('5/number/Recordings kept', 'Recordings kept', True),
        ('5/select/Wake word tone', 'Wake word tone', True),
        ('5/select/Follow-up tone', 'Follow-up tone', True),
        ('5/select/Wake word effect', 'Wake word effect', True),
        ('5/select/Thinking effect', 'Thinking effect', True),
        ('5/select/Replying effect', 'Replying effect', True),
    ]),
    ('Microphone', 'mdi:microphone', 'Mute never copies to other devices.', [
        ('2/switch/Microphone mute', 'Mute (this device only)', False),
        ('2/number/Microphone gain', 'Gain', True),
        ('2/number/Room sensitivity', 'Room sensitivity', True),
        ('2/number/Stop word sensitivity', 'Stop word sensitivity', True),
        ('2/switch/Microphone noise reduction', 'Noise reduction', True),
        ('2/switch/Microphone echo cancellation', 'Echo cancellation', True),
        ('2/select/Microphone echo canceller', 'Echo canceller', True),
        ('2/switch/Microphone leveling', 'Leveling', True),
        ('2/select/Microphone mixing', 'Mixing', True),
        ('2/select/Mute LED brightness', 'Mute LED brightness', True),
        ('2/switch/End of speech on the device', 'End of speech on the device', True),
    ]),
    ('Sound', 'mdi:speaker', 'Speaker tone and what music does while you talk.', [
        ('3/number/Bass', 'Bass', True),
        ('3/number/Treble', 'Treble', True),
        ('3/switch/Speaker EQ', 'Speaker EQ', True),
        ('3/select/Music during a turn', 'Music during a turn', True),
        ('3/number/Music ducking', 'Music ducking', True),
        ('3/switch/Duck after a near miss', 'Duck after a near miss', True),
        ('3/select/Voice resampling', 'Voice resampling', True),
        ('3/select/Quiet hours', 'Quiet hours', True),
        ('3/number/Night volume', 'Night volume', True),
        ('3/switch/Home Assistant sounds for muting and timers', 'HA sounds for muting & timers', True),
        ('3/select/Audio output', 'Audio output', True),
        ('0/switch/AirPlay', 'AirPlay (this device only)', False),
        ('0/switch/Spotify Connect', 'Spotify Connect (this device only)', False),
        ('0/switch/DLNA', 'DLNA (this device only)', False),
        ('3/switch/Sendspin', 'Sendspin / Music Assistant (this device only)', False),
    ]),
    ('Light ring', 'mdi:circle-slice-8', 'The LED ring toggle itself never copies.', [
        ('1/light/LED ring', 'LED ring (this device only)', False),
        ('1/select/Ring follows the room', 'Ring follows the room', True),
        ('1/select/Ring on failure', 'Ring on failure', True),
        ('1/select/Ring while muted', 'Ring while muted', True),
    ]),
    ('Screen', 'mdi:monitor', 'Devices with a screen only. Screen power and brightness never copy.', [
        ('0/light/Screen', 'Screen (this device only)', False),
        ('0/switch/Screen auto-brightness', 'Auto-brightness', True),
        ('0/number/Screensaver idle wait', 'Screensaver idle wait', True),
        ('0/select/Screensaver clock', 'Screensaver clock', True),
        ('0/select/Clock format', 'Clock format', True),
        ('0/select/Screen language', 'Screen language', True),
        ('0/select/Slideshow', 'Slideshow', True),
        ('0/number/Slideshow time per photo', 'Time per photo', True),
        ('0/switch/Slideshow shuffle', 'Shuffle', True),
        ('0/switch/Slideshow includes subfolders', 'Include subfolders', True),
        ('0/select/Screen at night', 'Screen at night', True),
        ('0/number/Night light brightness', 'Night light brightness', True),
        ('0/select/Night hours', 'Night hours (preset)', True),
        ('0/select/Night starts', 'Night starts', True),
        ('0/select/Night ends', 'Night ends', True),
        ('0/switch/Night mode', 'Night mode', True),
        ('0/select/Theme', 'Theme', True),
        ('0/select/Clock style', 'Clock style', True),
        ('0/switch/Weather art', 'Weather art', True),
        ('0/select/Night clock style', 'Night clock style', True),
        ('0/select/Clock position', 'Clock position', True),
        ('0/select/Date color', 'Date color', True),
        ('0/select/Turn screen', 'Screen turn animation', True),
        ('0/select/Camera time on screen', 'Camera stays on screen for', True),
        ('0/select/Now playing', 'Now playing screen', True),
        ('0/select/Now Playing follows', 'Now playing follows (this device only)', False),
        ('0/switch/Lyrics', 'Lyrics', True),
        ('0/switch/Weather animation', 'Weather animation', True),
        ('0/switch/Call button on the home screen', 'Call button on home screen', True),
        ('0/select/Dashboard', 'HA dashboard mode', True),
        ('0/select/Dashboard to show', 'Dashboard to show (this device only)', False),
        ('0/switch/Dashboard when idle', 'Show dashboard when idle', True),
        ('0/switch/Dashboard without its header', 'Hide dashboard header', True),
    ]),
    ('Weather & alarms', 'mdi:alarm', None, [
        ('0/select/Weather source', 'Weather source', True),
        ('0/select/Alarm sound', 'Alarm sound', True),
        ('0/number/Ring volume', 'Alarm & timer volume', True),
        ('0/number/Snooze length', 'Snooze length', True),
        ('0/select/Radar source', 'Radar source', True),
        ('0/switch/Weather alerts', 'Weather alerts', True),
        ('0/select/Sleep timer', 'Media sleep timer (this device only)', False),
        ('0/text_sensor/Next alarm', 'Next alarm', False),
        ('0/text_sensor/Timers', 'Timers', False),
        ('0/button/Snooze alarm', 'Snooze alarm', False),
        ('0/button/Stop alarm or timer', 'Stop alarm or timer', False),
        ('0/text_sensor/Missed ring', 'Missed ring', False),
    ]),
    ('Calendar on screen', 'mdi:calendar-clock', 'Devices with a screen only.', [
        ('0/switch/Event pop-ups', 'Event pop-ups', True),
        ('0/select/Pop up', 'Pop up', True),
        ('0/switch/Pop-up chime', 'Pop-up chime', True),
        ('0/select/All-day events', 'All-day events', True),
    ]),
    ('Phone & calls', 'mdi:phone', None, [
        ('0/switch/Allow Drop In', 'Allow Drop In (this device only)', False),
        ('0/switch/Talk through cameras', 'Talk through cameras (this device only)', False),
        ('0/switch/Camera sound', 'Camera sound', True),
        ('0/switch/Intercom do not disturb', 'Intercom do not disturb', True),
        ('0/select/Answer time on screen', 'Answer prompt stays for', True),
        ('0/text_sensor/Phone', 'Phone', False),
        ('0/text_sensor/Phone call', 'Current call', False),
        ('0/button/Answer call', 'Answer call', False),
        ('0/button/Hang up', 'Hang up', False),
    ]),
    ('Bluetooth', 'mdi:bluetooth', 'Per device only, never copied.', [
        ('0/switch/Bluetooth pairing', 'Pairing mode', False),
        ('0/switch/Bluetooth proxy', 'Bluetooth proxy', False),
        ('0/text_sensor/Bluetooth audio', 'Connected audio', False),
        ('0/button/Bluetooth reconnect', 'Reconnect', False),
        ('0/button/Bluetooth disconnect', 'Disconnect', False),
    ]),
    ('Firmware & system', 'mdi:chip', 'Only the metrics interval copies to other devices.', [
        ('0/update/Firmware', 'Firmware version', False),
        ('0/text_sensor/Update status', 'Update status', False),
        ('0/button/Check for updates', 'Check for updates', False),
        ('0/select/Update channel', 'Update channel (this device only)', False),
        ('0/switch/Install updates automatically', 'Install updates automatically (this device only)', False),
        ('0/number/Metrics interval', 'Metrics interval', True),
        ('0/number/Minimum CPU cores', 'Minimum CPU cores (this device only)', False),
        ('0/button/Purge cache', 'Purge cache', False),
        ('0/button/Restart', 'Restart this device', False),
    ]),
    ('Status', 'mdi:information-outline', None, [
        ('0/text_sensor/IP address', 'IP address', False),
        ('0/sensor/Wifi signal', 'Wi-Fi signal', False),
        ('0/sensor/CPU temperature', 'CPU temperature', False),
        ('0/sensor/Load average', 'Load average', False),
        ('0/sensor/Memory available', 'Memory available', False),
        ('0/sensor/Free space', 'Free space', False),
        ('0/text_sensor/Last heard', 'Last heard', False),
    ]),
    ('Remote access & security', 'mdi:shield-lock-outline',
     'Per device only, never copied. Keep these off unless you need them.', [
        ('0/switch/SSH', 'SSH', False),
        ('0/switch/Setup page', 'Setup page', False),
        ('0/switch/Screen web access', 'Screen web access', False),
        ('0/switch/Camera web access', 'Camera web access', False),
        ('0/switch/Settings lock', 'Settings lock (the PIN is set on the device)', False),
    ]),
]
# The three columns of the Settings view, top to bottom. A section not listed goes in the last column.
COLUMNS = [
    ['Firmware & system', 'Screen', 'Microphone', 'Bluetooth'],
    ['Status', 'Sound', 'Weather & alarms', 'Light ring', 'Voice sounds & effects'],
    ['Remote access & security', 'Voice', 'Phone & calls', 'Calendar on screen', 'Second wake word'],
]
# Sections left out of the Compare view (live values, not settings).
NO_COMPARE = {'Status', 'Bluetooth', 'Phone & calls'}

MAC = re.compile(r'^([0-9a-f]{2}:){5}[0-9a-f]{2}[-/]', re.I)
SELECT = 'input_select.echo_settings_device'
APPLY_ALL = 'input_boolean.echo_settings_apply_all'
LAST_SYNC = 'input_text.echo_settings_last_sync'
LAST_RUN = 'input_text.echo_update_last_run'


# ---------------------------------------------------------------------------------------------------
# A small WebSocket client (RFC 6455), enough for Home Assistant's API.
class WebSocket:
    def __init__(self, url, timeout=30):
        u = urllib.parse.urlparse(url)
        if u.scheme not in ('ws', 'wss'):
            raise Fail('not a WebSocket address: ' + url)
        port = u.port or (443 if u.scheme == 'wss' else 80)
        try:
            sock = socket.create_connection((u.hostname, port), timeout=timeout)
            if u.scheme == 'wss':
                sock = ssl.create_default_context().wrap_socket(sock, server_hostname=u.hostname)
        except (OSError, ssl.SSLError) as e:
            raise Fail("Can't reach %s:%d (%s)" % (u.hostname, port, e))
        self.sock = sock
        self.rfile = sock.makefile('rb')
        key = base64.b64encode(secrets.token_bytes(16)).decode()
        sock.sendall(('GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n'
                      'Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n'
                      % (u.path or '/', u.netloc, key)).encode())
        status = self.rfile.readline().decode('latin-1').strip()
        headers = {}
        while True:
            line = self.rfile.readline().decode('latin-1').strip()
            if not line:
                break
            k, _, v = line.partition(':')
            headers[k.strip().lower()] = v.strip()
        if ' 101 ' not in status:
            raise Fail('Home Assistant did not accept the connection (%s)' % status)
        want = base64.b64encode(hashlib.sha1((key + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest()).decode()
        if headers.get('sec-websocket-accept') != want:
            raise Fail('The server is not a WebSocket server')

    def _send(self, opcode, payload):
        head = bytes([0x80 | opcode])
        n = len(payload)
        if n < 126:
            head += bytes([0x80 | n])
        elif n < 65536:
            head += bytes([0x80 | 126]) + struct.pack('>H', n)
        else:
            head += bytes([0x80 | 127]) + struct.pack('>Q', n)
        mask = secrets.token_bytes(4)
        body = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
        self.sock.sendall(head + mask + body)

    def send(self, text):
        self._send(1, text.encode())

    def _read(self, n):
        data = self.rfile.read(n)
        if len(data) != n:
            raise Fail('Home Assistant closed the connection')
        return data

    def recv(self):
        text = b''
        while True:
            b0, b1 = self._read(2)
            opcode, n = b0 & 0x0F, b1 & 0x7F
            if n == 126:
                n = struct.unpack('>H', self._read(2))[0]
            elif n == 127:
                n = struct.unpack('>Q', self._read(8))[0]
            payload = self._read(n)  # the server does not mask
            if opcode == 8:
                raise Fail('Home Assistant closed the connection')
            if opcode == 9:
                self._send(10, payload)
            elif opcode in (0, 1):
                text += payload
                if b0 & 0x80:
                    return text.decode()

    def close(self):
        try:
            self._send(8, b'')
        except OSError:
            pass
        self.sock.close()


class HomeAssistant:
    """The WebSocket API: sign in once, then call commands."""

    def __init__(self, base_url, token):
        u = urllib.parse.urlparse(base_url if '://' in base_url else 'http://' + base_url)
        scheme = 'wss' if u.scheme == 'https' else 'ws'
        self.ws = WebSocket('%s://%s%s/api/websocket' % (scheme, u.netloc, u.path.rstrip('/')))
        self.n = 0
        first = json.loads(self.ws.recv())
        if first.get('type') != 'auth_required':
            raise Fail('Unexpected answer from Home Assistant: ' + first.get('type', '?'))
        self.ws.send(json.dumps({'type': 'auth', 'access_token': token}))
        answer = json.loads(self.ws.recv())
        if answer.get('type') != 'auth_ok':
            raise Fail('Home Assistant refused the token. Make a new long-lived access token on your '
                       'profile page and try again.')

    def call(self, type, **fields):
        self.n += 1
        self.ws.send(json.dumps(dict(fields, id=self.n, type=type)))
        while True:
            r = json.loads(self.ws.recv())
            if r.get('id') == self.n:
                if not r.get('success'):
                    raise Fail('%s failed: %s' % (type, (r.get('error') or {}).get('message', r)))
                return r.get('result')

    def close(self):
        self.ws.close()


# ---------------------------------------------------------------------------------------------------
# Devices and entities
def is_techo5(dev):
    return dev.get('manufacturer') == 'TECHO5' or 'TECHO5' in (dev.get('model') or '')


def short_name(name):
    word = re.split(r"[\s']+", name.strip())[0] or name
    return word[:8]


def slug(key):
    return re.sub(r'[^a-z0-9]+', '_', key.lower()).strip('_')


def find_devices(devices, entities):
    """The TECHO5 devices, and a table of key -> {device name: entity id}.

    Returns (names, panels, table). ESPHome's own devices only (Music Assistant lists the same devices
    again as players). Sub-devices, such as the microphone, are folded into the device they belong to.
    """
    t5 = {d['id']: d for d in devices
          if is_techo5(d) and all(i[0] == 'esphome' for i in d.get('identifiers') or [])}

    def root(dev_id):
        seen = set()
        while t5[dev_id].get('via_device_id') in t5 and dev_id not in seen:
            seen.add(dev_id)
            dev_id = t5[dev_id]['via_device_id']
        return dev_id

    names, used = {}, set()
    tops = sorted((d for d in t5.values() if root(d['id']) == d['id'] and not d.get('disabled_by')),
                  key=lambda d: (d.get('name_by_user') or d.get('name') or '').lower())
    for d in tops:
        name = d.get('name_by_user') or d.get('name') or d['id'][:8]
        base, i = name, 2
        while name in used:
            name, i = '%s (%d)' % (base, i), i + 1
        used.add(name)
        names[d['id']] = name
    panels = {names[d['id']] for d in tops if 'Panel' in (d.get('model') or '')}
    table = {}
    for e in entities:
        if e.get('disabled_by') or e.get('device_id') not in t5 or not e.get('unique_id'):
            continue
        r = root(e['device_id'])
        if r in names:
            table.setdefault(MAC.sub('', e['unique_id']), {})[names[r]] = e['entity_id']
    return sorted(names.values(), key=str.lower), panels, table


def short_names(names):
    shorts = [short_name(n) for n in names]
    # Two that would share a first word go by their last word instead.
    return [n.split()[-1][:8] if shorts.count(s) > 1 else s for n, s in zip(names, shorts)]


# ---------------------------------------------------------------------------------------------------
# Building the package and the dashboard
def build(names, panels, table, card_mod=False, copy_panels=False):
    """Returns (package, dashboard) as plain dicts and lists."""
    skip_sync = set() if copy_panels else set(panels)
    shorts = dict(zip(names, short_names(names)))
    glass = ({'card_mod': {'style': 'ha-card {\n  background: rgba(60, 60, 60, 0.35) !important;\n'
                                     '  border-radius: 18px !important;\n}\n'}} if card_mod else {})

    def mod(card):
        card.update(glass)
        return card

    def row(dev, key):
        return table.get(key, {}).get(dev)

    ups = {d: row(d, '0/update/Firmware') for d in names if row(d, '0/update/Firmware')}
    checks = {d: row(d, '0/button/Check for updates') for d in names if row(d, '0/button/Check for updates')}

    sync = {}
    for _, _, _, rows in SECTIONS:
        for key, _, copy in rows:
            if copy and key in table:
                m = {d: table[key][d] for d in names if d in table[key] and d not in skip_sync}
                if m:
                    sync[slug(key)] = m
    rev = {e: k for k, m in sync.items() for e in m.values()}
    left_out = sorted(skip_sync)

    now = "{{ now().strftime('%-I:%M %p') }}"
    package = {
        'input_select': {'echo_settings_device': {
            'name': 'Echo settings device', 'icon': 'mdi:speaker-message', 'options': names}},
        'input_boolean': {'echo_settings_apply_all': {
            'name': 'Echo settings: apply to all devices', 'icon': 'mdi:checkbox-multiple-marked-outline',
            'initial': False}},
        'input_text': {
            'echo_settings_last_sync': {'name': 'Echo settings last sync', 'max': 255, 'initial': ''},
            'echo_update_last_run': {'name': 'Echo update last run', 'max': 255, 'initial': ''}},
        'script': {},
    }
    if rev:
        package['automation'] = [{
            'id': 'echo_settings_apply_to_all',
            'alias': 'Echo Settings - copy a change to all devices',
            'description': "Generated by tools/ha/echo_settings.py. When 'All devices' is on, a setting you "
                           'change on the selected device in the dashboard is copied to the other devices. '
                           'Only changes made by a person (not the device itself) are copied.',
            'mode': 'queued', 'max': 100,
            'variables': {'echo_map': sync, 'echo_rev': rev},
            'triggers': [{'trigger': 'state', 'entity_id': sorted(rev),
                          'not_from': ['unavailable', 'unknown'], 'not_to': ['unavailable', 'unknown']}],
            'conditions': [
                {'condition': 'state', 'entity_id': APPLY_ALL, 'state': 'on'},
                {'condition': 'template', 'value_template': '{{ trigger.to_state.context.user_id is not none }}'},
                {'condition': 'template',
                 'value_template': "{{ echo_map[echo_rev[trigger.entity_id]].get(states('%s')) == trigger.entity_id }}" % SELECT},
            ],
            'actions': [{'action': 'script.echo_settings_apply',
                         'data': {'source': '{{ trigger.entity_id }}', 'setting': '{{ echo_rev[trigger.entity_id] }}'}}],
        }]
    scripts = package['script']
    scripts['echo_settings_apply'] = {
        'alias': 'Echo Settings - apply one setting to all',
        'mode': 'queued', 'max': 100,
        'fields': {'source': {'description': 'entity to copy from'},
                   'setting': {'description': 'setting key'}},
        'variables': {'echo_map': sync},
        'sequence': [
            {'variables': {
                'domain': "{{ source.split('.')[0] }}",
                'value': '{{ states(source) }}',
                'plan': (
                    "{% set ns = namespace(go=[], skip=[]) %}"
                    "{% for dev, e in echo_map[setting].items() if e != source %}"
                    "{% set s = states(e) %}"
                    "{% if s in ['unavailable', 'unknown'] %}{% set ns.skip = ns.skip + [dev ~ ' offline'] %}"
                    "{% elif s == value %}"
                    "{% elif domain == 'select' and value not in (state_attr(e, 'options') or []) %}"
                    "{% set ns.skip = ns.skip + [dev ~ ' has no ' ~ value] %}"
                    "{% elif domain == 'number' and not (state_attr(e, 'min') | float(0) <= value | float(0) <= state_attr(e, 'max') | float(0)) %}"
                    "{% set ns.skip = ns.skip + [dev ~ ' out of range'] %}"
                    "{% else %}{% set ns.go = ns.go + [e] %}{% endif %}"
                    "{% endfor %}{{ {'go': ns.go, 'skip': ns.skip} }}")}},
            {'if': [{'condition': 'template', 'value_template': '{{ plan.go | count > 0 }}'}],
             'then': [{'choose': [
                 {'conditions': "{{ domain == 'number' }}",
                  'sequence': [{'action': 'number.set_value', 'continue_on_error': True,
                                'target': {'entity_id': '{{ plan.go }}'}, 'data': {'value': '{{ value }}'}}]},
                 {'conditions': "{{ domain == 'select' }}",
                  'sequence': [{'action': 'select.select_option', 'continue_on_error': True,
                                'target': {'entity_id': '{{ plan.go }}'}, 'data': {'option': '{{ value }}'}}]},
                 {'conditions': "{{ domain == 'switch' and value in ['on', 'off'] }}",
                  'sequence': [{'action': 'switch.turn_{{ value }}', 'continue_on_error': True,
                                'target': {'entity_id': '{{ plan.go }}'}}]},
             ]}]},
            {'action': 'input_text.set_value', 'target': {'entity_id': LAST_SYNC},
             'data': {'value': (
                 "{% set name = state_attr(source, 'friendly_name') %}"
                 "{{ (now().strftime('%-I:%M %p') ~ ' - ' ~ name ~ ' = ' ~ value ~ ' -> '"
                 " ~ (plan.go | count) ~ ' updated'"
                 " ~ ((', skipped: ' ~ plan.skip | join(', ')) if plan.skip else ''))[:255] }}")}},
        ],
    }
    scripts['echo_settings_copy_all'] = {
        'alias': 'Echo Settings - copy all settings from selected device',
        'icon': 'mdi:content-copy', 'mode': 'single',
        'variables': {'echo_map': sync},
        'sequence': [
            {'variables': {'dev': "{{ states('%s') }}" % SELECT}},
            {'repeat': {
                'for_each': "{{ echo_map | dictsort | selectattr('1.' ~ dev, 'defined') | map(attribute='0') | list }}",
                'sequence': [{'action': 'script.echo_settings_apply', 'continue_on_error': True,
                              'data': {'setting': '{{ repeat.item }}', 'source': '{{ echo_map[repeat.item][dev] }}'}}]}},
            {'action': 'input_text.set_value', 'target': {'entity_id': LAST_SYNC},
             'data': {'value': now + ' - copied all settings from {{ dev }} to the other devices'}},
        ],
    }
    if checks:
        scripts['echo_check_all_updates'] = {
            'alias': 'Echo Settings - check all devices for updates',
            'icon': 'mdi:cloud-search-outline', 'mode': 'single',
            'sequence': [
                {'action': 'button.press', 'continue_on_error': True,
                 'target': {'entity_id': sorted(checks.values())}},
                {'action': 'input_text.set_value', 'target': {'entity_id': LAST_RUN},
                 'data': {'value': now + ' - checked all devices for updates'}}]}
    if ups:
        scripts['echo_update_all'] = {
            'alias': 'Echo Settings - update all devices',
            'icon': 'mdi:cloud-download-outline', 'mode': 'single',
            'sequence': [
                {'variables': {'pending': "{{ [%s] | select('is_state', 'on') | list }}"
                               % ', '.join(repr(e) for e in sorted(ups.values()))}},
                {'if': [{'condition': 'template', 'value_template': '{{ pending | count == 0 }}'}],
                 'then': [
                     {'action': 'input_text.set_value', 'target': {'entity_id': LAST_RUN},
                      'data': {'value': now + ' - no updates available; nothing to do'}},
                     {'stop': 'all devices already up to date'}]},
                {'action': 'input_text.set_value', 'target': {'entity_id': LAST_RUN},
                 'data': {'value': now + ' - starting {{ pending | count }} device(s), 3 seconds apart...'}},
                {'repeat': {'for_each': '{{ pending }}', 'sequence': [
                    {'action': 'update.install', 'continue_on_error': True,
                     'target': {'entity_id': '{{ repeat.item }}'}},
                    {'delay': {'seconds': 3}}]}},
                # They download, install and restart on their own; just wait for the finish.
                {'wait_template': "{{ pending | select('is_state', 'on') | list | count == 0 }}",
                 'timeout': {'minutes': 15}, 'continue_on_timeout': True},
                {'action': 'input_text.set_value', 'target': {'entity_id': LAST_RUN},
                 'data': {'value': (
                     "{% set left = pending | select('is_state', 'on') | list %}"
                     "{{ (now().strftime('%-I:%M %p') ~ ' - updated ' ~ (pending | count - left | count)"
                     " ~ ' of ' ~ (pending | count) ~ ' device(s)'"
                     " ~ ((', still pending: ' ~ left | map('state_attr', 'friendly_name') | join(', ')) if left else ''))[:255] }}")}},
            ]}

    # ---------------- dashboard
    def entity_rows(dev, rows):
        return [{'entity': row(dev, k), 'name': label} for k, label, _ in rows if row(dev, k)]

    note = ('Pick a device and change its settings below. Turn on "All devices" to copy each change to '
            'every other device that has the setting' + (' (%s are never included).' % ', '.join(left_out)
                                                         if left_out else '.'))
    top_cards = [
        {'type': 'heading', 'heading': 'Echo Settings', 'icon': 'mdi:speaker-message'},
        {'type': 'heading', 'heading_style': 'subtitle', 'heading': note},
        mod({'type': 'tile', 'entity': SELECT, 'name': 'Device', 'icon': 'mdi:speaker', 'hide_state': True,
             'features': [{'type': 'select-options'}], 'features_position': 'inline',
             'grid_options': {'columns': 6}}),
        mod({'type': 'tile', 'entity': APPLY_ALL, 'name': 'All devices', 'state_content': 'state',
             'grid_options': {'columns': 3}, 'tap_action': {'action': 'toggle'}}),
        mod({'type': 'tile', 'entity': 'script.echo_settings_copy_all', 'name': 'Copy all to others',
             'icon': 'mdi:content-copy', 'hide_state': True, 'grid_options': {'columns': 3},
             'tap_action': {'action': 'perform-action', 'perform_action': 'script.echo_settings_copy_all',
                            'confirmation': {'text': 'Copy every copyable setting from the selected device to all '
                                             'the other devices? ' + ('Excludes %s. ' % ', '.join(left_out) if left_out else '')
                                             + '(Mute, screen, LED ring, Bluetooth, SSH, setup and web access and CPU '
                                               'cores are never copied.)'}}}),
        mod({'type': 'markdown',
             'visibility': [{'condition': 'state', 'entity': APPLY_ALL, 'state': 'on'}],
             'content': "**All devices is ON.** Changes you make below to **{{ states('%s') }}** are copied to the "
                        "other devices. Settings marked *(this device only)* are never copied%s"
                        % (SELECT, (', and %s are left out entirely.' % ', '.join(left_out)) if left_out else '.')}),
        mod({'type': 'markdown',
             'visibility': [{'condition': 'state', 'entity': LAST_SYNC, 'state_not': ''}],
             'content': "<small>Last copy: {{ states('%s') }}</small>" % LAST_SYNC}),
    ]
    top = {'type': 'grid', 'column_span': 3, 'cards': top_cards}

    extra = {}
    if ups:
        up_list = ', '.join(repr(e) for e in sorted(ups.values()))
        cards = [mod({'type': 'markdown', 'content':
                      "{% set ups = [" + up_list + "] %}{% set ns = namespace(p=[]) %}"
                      "{% for e in ups %}{% if states(e) == 'on' %}"
                      "{% set ns.p = ns.p + [(state_attr(e, 'friendly_name') or '') | replace(' Firmware', '')"
                      " ~ ' ' ~ (state_attr(e, 'latest_version') or '')] %}{% endif %}{% endfor %}"
                      "{% if ns.p %}**{{ ns.p | count }} of {{ ups | count }} devices have an update:** "
                      "{{ ns.p | join(', ') }}{% else %}**All {{ ups | count }} devices are up to date.**{% endif %}"}),
                 mod({'type': 'tile', 'entity': 'script.echo_update_all', 'name': 'Update all devices',
                      'icon': 'mdi:cloud-download-outline', 'hide_state': True, 'grid_options': {'columns': 6},
                      'tap_action': {'action': 'perform-action', 'perform_action': 'script.echo_update_all',
                                     'confirmation': {'text': 'Install firmware updates on every device that has one? '
                                                      'They start 3 seconds apart and each device restarts, so voice '
                                                      'and audio drop out on it for a minute.'}}})]
        if checks:
            cards.append(mod({'type': 'tile', 'entity': 'script.echo_check_all_updates',
                              'name': 'Check all for updates', 'icon': 'mdi:cloud-search-outline',
                              'hide_state': True, 'grid_options': {'columns': 6},
                              'tap_action': {'action': 'perform-action',
                                             'perform_action': 'script.echo_check_all_updates'}}))
        cards.append(mod({'type': 'markdown',
                          'visibility': [{'condition': 'state', 'entity': LAST_RUN, 'state_not': ''}],
                          'content': "<small>Updates: {{ states('%s') }}</small>" % LAST_RUN}))
        extra['Firmware & system'] = cards

    by_title = {t: (i, sub, rows) for t, i, sub, rows in SECTIONS}
    listed = {t for col in COLUMNS for t in col}
    columns = [list(c) for c in COLUMNS]
    columns[-1] += [t for t, _, _, _ in SECTIONS if t not in listed]
    sections = [top]
    for col in columns:
        cards = []
        for title in col:
            icon, sub, rows = by_title[title]
            have = [d for d in names if entity_rows(d, rows)]
            if not have:
                continue
            vis = None if len(have) == len(names) else [{'condition': 'or', 'conditions': [
                {'condition': 'state', 'entity': SELECT, 'state': d} for d in have]}]
            heads = [{'type': 'heading', 'heading': title, 'icon': icon}]
            if sub:
                heads.append({'type': 'heading', 'heading': sub, 'heading_style': 'subtitle'})
            for h in heads:
                if vis:
                    h['visibility'] = vis
                cards.append(h)
            cards.extend(extra.get(title, []))
            for d in have:
                cards.append(mod({'type': 'entities', 'entities': entity_rows(d, rows), 'state_color': True,
                                  'visibility': [{'condition': 'state', 'entity': SELECT, 'state': d}]}))
        if cards:
            sections.append({'type': 'grid', 'cards': cards})

    # Compare view: one table per section; (!) marks settings that differ between devices.
    cmp_sections = []
    for title, icon, _, rows in SECTIONS:
        if title in NO_COMPARE:
            continue
        rows = [r for r in rows if r[0] in table and re.match(r'.*/(number|select|switch|light|update)/|[a-z_0-9]+$', r[0])]
        devs = [d for d in names if any(d in table[k] for k, _, _ in rows)]
        if not rows or not devs:
            continue
        head = '| Setting | ' + ' | '.join(shorts[d] for d in devs) + ' |\n|---|' + '---|' * len(devs) + '\n'
        body = ''
        for key, label, _ in rows:
            lst = '[' + ', '.join("'%s'" % table[key][d] if d in table[key] else 'none' for d in devs) + ']'
            body += (
                '{% set ids = ' + lst + ' %}'
                '{% set ns = namespace(v=[]) %}{% for e in ids %}'
                "{% if not e %}{% set s = '-' %}"
                "{% elif e.startswith('update.') %}{% set s = state_attr(e, 'installed_version') or states(e) %}"
                "{% else %}{% set s = states(e) %}"
                "{% if s | float(none) is not none %}{% set f = s | float %}{% set s = f | int if f == f | int else f %}{% endif %}"
                '{% endif %}'
                '{% set ns.v = ns.v + [s] %}{% endfor %}'
                "{% set real = ns.v | reject('in', ['-', 'unavailable', 'unknown']) | unique | list %}"
                "| {{ '(!) ' if real | count > 1 else '' }}" + label.replace(' (this device only)', '') +
                " | {{ ns.v | join(' | ') }} |\n")
        cmp_sections.append({'type': 'grid', 'column_span': 3, 'cards': [
            {'type': 'heading', 'heading': title, 'icon': icon},
            mod({'type': 'markdown', 'content': head + body})]})

    dashboard = {'title': 'Echo Settings', 'views': [
        {'title': 'Settings', 'path': 'settings', 'icon': 'mdi:tune', 'type': 'sections',
         'max_columns': 3, 'sections': sections},
        {'title': 'Compare', 'path': 'compare', 'icon': 'mdi:table-large', 'type': 'sections',
         'max_columns': 1,
         'sections': [{'type': 'grid', 'column_span': 3, 'cards': [
             {'type': 'heading', 'heading': 'Compare all devices', 'icon': 'mdi:table-large'},
             {'type': 'heading', 'heading_style': 'subtitle',
              'heading': "(!) = the devices don't all match. - = that device doesn't have this setting."}]}]
         + cmp_sections}]}
    return package, dashboard


# ---------------------------------------------------------------------------------------------------
# YAML out (no PyYAML; JSON strings are valid YAML, so anything unusual is quoted that way)
_PLAIN = re.compile(r'^[A-Za-z_][A-Za-z0-9_./ -]*$')
_RESERVED = {'true', 'false', 'null', 'yes', 'no', 'on', 'off', 'y', 'n', '~'}


def scalar(v):
    if v is True:
        return 'true'
    if v is False:
        return 'false'
    if v is None:
        return 'null'
    if isinstance(v, (int, float)):
        return repr(v)
    if _PLAIN.match(v) and v.lower() not in _RESERVED and not v.endswith(' '):
        return v
    return json.dumps(v, ensure_ascii=False)


def _lines(o, indent):
    pad = ' ' * indent
    out = []
    if isinstance(o, dict):
        for k, v in o.items():
            if isinstance(v, (dict, list)) and v:
                out.append('%s%s:' % (pad, scalar(k)))
                out += _lines(v, indent + 2)
            else:
                out.append('%s%s: %s' % (pad, scalar(k), '{}' if isinstance(v, dict) else
                                         '[]' if isinstance(v, list) else scalar(v)))
    else:
        for v in o:
            if isinstance(v, (dict, list)) and v:
                sub = _lines(v, indent + 2)
                out.append(pad + '- ' + sub[0][indent + 2:])
                out += sub[1:]
            else:
                out.append('%s- %s' % (pad, '{}' if isinstance(v, dict) else '[]' if isinstance(v, list) else scalar(v)))
    return out


def to_yaml(o):
    return '\n'.join(_lines(o, 0)) + '\n'


# ---------------------------------------------------------------------------------------------------
def main(argv=None):
    ap = argparse.ArgumentParser(description='Make a settings dashboard and package for your TECHO5 devices.')
    ap.add_argument('--url', default=os.environ.get('HA_URL', 'http://homeassistant.local:8123'),
                    help='Home Assistant address (or the HA_URL environment variable)')
    ap.add_argument('--out', default='.', help='folder for the two files (default: here)')
    ap.add_argument('--card-mod', action='store_true',
                    help='add card_mod styling (needs the card_mod add-on from HACS)')
    ap.add_argument('--include-panels', action='store_true',
                    help='also copy settings to and from TECHO5 Panel devices (left out by default)')
    ap.add_argument('--push', action='store_true',
                    help='also save the dashboard in Home Assistant at /%s (the package is always manual)'
                         % DASHBOARD_PATH)
    ap.add_argument('--yes', action='store_true', help='with --push: replace an existing dashboard without asking')
    args = ap.parse_args(argv)

    token = os.environ.get('HA_TOKEN') or getpass.getpass('Long-lived access token (not shown, not saved): ')
    if not token.strip():
        raise Fail('No token. Make one on your Home Assistant profile page (Security, bottom of the page).')
    ha = HomeAssistant(args.url, token.strip())
    try:
        devices = ha.call('config/device_registry/list')
        entities = ha.call('config/entity_registry/list')
        names, panels, table = find_devices(devices, entities)
        if not names:
            raise Fail('No TECHO5 devices found in Home Assistant.')
        package, dashboard = build(names, panels, table, args.card_mod, args.include_panels)
        os.makedirs(args.out, exist_ok=True)
        dash_file = os.path.join(args.out, 'echo_settings_dashboard.yaml')
        pkg_file = os.path.join(args.out, 'echo_settings_package.yaml')
        with open(dash_file, 'w', encoding='utf-8') as f:
            f.write('# Made by tools/ha/echo_settings.py. Paste into a dashboard: Raw configuration editor.\n'
                    + to_yaml(dashboard))
        with open(pkg_file, 'w', encoding='utf-8') as f:
            f.write('# Made by tools/ha/echo_settings.py. Copy to /config/packages/echo_settings.yaml\n'
                    + to_yaml(package))
        print('Found %d device(s): %s' % (len(names), ', '.join(names)))
        if panels:
            print('Panel app devices %s: shown, but %s.' % (
                ', '.join(sorted(panels)), 'copied to and from' if args.include_panels else 'left out of copy-to-all'))
        print('Wrote', dash_file)
        print('Wrote', pkg_file)
        if args.push:
            push(ha, dashboard, args.yes)
    finally:
        ha.close()
    return 0


def push(ha, dashboard, yes):
    existing = {d['url_path'] for d in ha.call('lovelace/dashboards/list')}
    if DASHBOARD_PATH in existing:
        if not yes and input('Replace the dashboard at /%s? [y/N] ' % DASHBOARD_PATH).strip().lower() != 'y':
            print('Left the dashboard in Home Assistant as it was.')
            return
    else:
        ha.call('lovelace/dashboards/create', url_path=DASHBOARD_PATH, title='Echo Settings',
                icon='mdi:speaker-message', show_in_sidebar=True, require_admin=False, mode='storage')
    ha.call('lovelace/config/save', url_path=DASHBOARD_PATH, config=dashboard)
    print('Saved the dashboard at /%s. Install the package too, or its cards will show errors.' % DASHBOARD_PATH)


if __name__ == '__main__':
    try:
        sys.exit(main())
    except Fail as e:
        print('Error:', e, file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        sys.exit(130)
