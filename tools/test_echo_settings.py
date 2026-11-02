#!/usr/bin/env python3
"""Offline tests for tools/ha/echo_settings.py: a fake registry, no Home Assistant.

    python3 tools/test_echo_settings.py
"""
import importlib.util
import os
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location('echo_settings', os.path.join(HERE, 'ha', 'echo_settings.py'))
es = importlib.util.module_from_spec(spec)
spec.loader.exec_module(es)


def dev(i, name, model='Echo Show 8', mfr='TECHO5', via=None, ids=None):
    return {'id': i, 'name': name, 'name_by_user': None, 'manufacturer': mfr, 'model': model,
            'via_device_id': via, 'identifiers': ids or [['esphome', i]]}


def ent(dev_id, eid, uid, disabled=None):
    return {'device_id': dev_id, 'entity_id': eid, 'unique_id': 'aa:bb:cc:dd:ee:0%s-%s' % (dev_id[-1], uid),
            'disabled_by': disabled}


DEVICES = [
    dev('d1', 'Kitchen'), dev('d2', 'Office', model='TECHO5 Echo Dot', mfr='Someone'),
    dev('d3', 'Hall Panel', model='TECHO5 Panel'),
    dev('m1', 'Kitchen microphone', via='d1'),
    dev('x1', 'Other', mfr='Acme', model='Plug'),
    dev('x2', 'Player', ids=[['music_assistant', 'x']]),
]
ENTITIES = [
    ent('d1', 'number.kitchen_gain', '2/number/Microphone gain'),
    ent('d2', 'number.office_gain', '2/number/Microphone gain'),
    ent('d3', 'number.hall_gain', '2/number/Microphone gain'),
    ent('m1', 'switch.kitchen_mute', '2/switch/Microphone mute'),
    ent('d1', 'light.kitchen_screen', '0/light/Screen'),
    ent('d1', 'update.kitchen_fw', '0/update/Firmware'),
    ent('d1', 'button.kitchen_check', '0/button/Check for updates'),
    ent('d2', 'button.office_check', '0/button/Check for updates'),
    ent('d2', 'number.office_old', '2/number/Room sensitivity', disabled='user'),
    ent('x1', 'switch.plug', '0/switch/Plug'),
]


class Devices(unittest.TestCase):
    def setUp(self):
        self.names, self.panels, self.table = es.find_devices(DEVICES, ENTITIES)

    def test_filter(self):
        self.assertEqual(self.names, ['Hall Panel', 'Kitchen', 'Office'])
        self.assertEqual(self.panels, {'Hall Panel'})

    def test_subdevice_folds_in(self):
        self.assertEqual(self.table['2/switch/Microphone mute'], {'Kitchen': 'switch.kitchen_mute'})

    def test_mac_stripped_and_disabled_skipped(self):
        self.assertIn('2/number/Microphone gain', self.table)
        self.assertNotIn('2/number/Room sensitivity', self.table)

    def test_duplicate_names(self):
        names, _, _ = es.find_devices([dev('d1', 'Echo'), dev('d2', 'Echo')], [])
        self.assertEqual(names, ['Echo', 'Echo (2)'])


class Build(unittest.TestCase):
    def setUp(self):
        self.names, self.panels, self.table = es.find_devices(DEVICES, ENTITIES)
        self.pkg, self.dash = es.build(self.names, self.panels, self.table)

    def test_panel_left_out_of_sync(self):
        m = self.pkg['automation'][0]['variables']['echo_map']['2_number_microphone_gain']
        self.assertEqual(m, {'Kitchen': 'number.kitchen_gain', 'Office': 'number.office_gain'})

    def test_panel_included_on_request(self):
        pkg = es.build(self.names, self.panels, self.table, copy_panels=True)[0]
        self.assertIn('Hall Panel', pkg['automation'][0]['variables']['echo_map']['2_number_microphone_gain'])

    def test_never_copied_not_in_map(self):
        self.assertNotIn('2_switch_microphone_mute', self.pkg['automation'][0]['variables']['echo_map'])

    def test_missing_entities_hidden(self):
        cards = [c for s in self.dash['views'][0]['sections'] for c in s['cards'] if c['type'] == 'entities']
        screens = [c for c in cards if any(e['entity'] == 'light.kitchen_screen' for e in c['entities'])]
        self.assertEqual(len(screens), 1)
        self.assertEqual(screens[0]['visibility'][0]['state'], 'Kitchen')
        self.assertFalse(any('office_screen' in str(c) for c in cards))

    def test_helpers_and_scripts(self):
        self.assertEqual(self.pkg['input_select']['echo_settings_device']['options'], self.names)
        for s in ('echo_settings_apply', 'echo_settings_copy_all', 'echo_check_all_updates', 'echo_update_all'):
            self.assertIn(s, self.pkg['script'])

    def test_card_mod_only_on_request(self):
        self.assertNotIn('card_mod', es.to_yaml(self.dash))
        dash = es.build(self.names, self.panels, self.table, card_mod=True)[1]
        self.assertIn('card_mod', es.to_yaml(dash))


class Yaml(unittest.TestCase):
    def test_scalars(self):
        self.assertEqual(es.scalar('on'), '"on"')
        self.assertEqual(es.scalar('Kitchen'), 'Kitchen')
        self.assertEqual(es.scalar('{{ x }}'), '"{{ x }}"')
        self.assertEqual(es.scalar(False), 'false')

    def test_nesting(self):
        o = {'a': [{'b': 1, 'c': [1, 2]}, 'x'], 'd': {}, 'e': {'f': 'y z'}}
        self.assertEqual(es.to_yaml(o), 'a:\n  - b: 1\n    c:\n      - 1\n      - 2\n  - x\nd: {}\ne:\n  f: y z\n')

    def test_quoting(self):
        self.assertEqual(es.scalar('a\nb "q"'), '"a\\nb \\"q\\""')


if __name__ == '__main__':
    unittest.main()
