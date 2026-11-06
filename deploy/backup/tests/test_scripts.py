#!/usr/bin/env python3
import base64
import json
import os
import pathlib
import subprocess
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent.parent


class ScriptsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='chatisp-script-test-')
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.fake = self.root / 'compose'
        self.fake.write_text('''#!/usr/bin/env python3
import json,os,sys
with open(os.environ['CALLS'], 'a') as out: out.write(json.dumps(sys.argv[1:])+'\\n')
source=' '.join(sys.argv[1:])
if os.environ.get('DENY_LOCK') and 'mkdir /mirror/.operation-lock' in source: sys.exit(1)
if os.environ.get('DENY_AUDIT') and 'successes.log' in source: sys.exit(1)
if 'printf' in source and '$CHATISP_DATA_KEYS' in source:
 print('CHATISP_DATA_KEYS='+os.environ['SYNTHETIC_KEY'])
 print('CHATISP_DATA_KEY_VERSION=1')
if 'cat >' in source: sys.stdin.buffer.read()
''')
        self.fake.chmod(0o700)
        self.pass_file = self.root / 'pass'
        self.pass_file.write_text('synthetic-passphrase')
        self.envfile = self.root / 'env'
        key = '1:' + base64.b64encode(bytes(range(32))).decode()
        self.envfile.write_text('CHATISP_DATA_KEYS=' + key + '\nCHATISP_DATA_KEY_VERSION=1\n')
        self.calls = self.root / 'calls'
        self.status = self.root / 'status.prom'
        self.env = dict(os.environ, COMPOSE_CMD=f'{self.fake} --env-file {self.envfile}',
                        BACKUP_INSTALLATION='fixture-a', CALLS=str(self.calls), SYNTHETIC_KEY=key,
                        BACKUP_KEYS_PASSPHRASE_FILE=str(self.pass_file), BACKUP_STATUS_FILE=str(self.status))

    def run_script(self, script, *args, **extra):
        return subprocess.run(['bash', str(HERE / script), *args], env=dict(self.env, **extra), capture_output=True, text=True)

    def call_list(self):
        return [json.loads(line) for line in self.calls.read_text().splitlines()]

    def test_systemd_instances_invoke_correct_pgbackrest_type(self):
        for kind in ('full', 'diff', 'incr'):
            result = self.run_script('chatisp-backup.sh', kind)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertTrue(any('--type=' + kind in call and 'backup' in call for call in self.call_list()))
            for component in ('postgres', 'attachments', 'keys', 'ledger'):
                self.assertIn('installation="fixture-a",kind="' + component + '"', self.status.read_text())
            timer = (HERE / 'systemd' / ('chatisp-backup-' + kind + '.timer')).read_text()
            self.assertIn('Unit=chatisp-backup@' + kind + '.service', timer)
        self.assertIn('chatisp-backup.sh %i', (HERE / 'systemd/chatisp-backup@.service').read_text())

    def test_ledger_timer_exports_and_records_ledger(self):
        result = self.run_script('chatisp-backup.sh', 'ledger')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('kind="ledger"', self.status.read_text())
        self.assertFalse(any('pgbackrest' in call for call in self.call_list()))
        self.assertIn('Unit=chatisp-backup@ledger.service', (HERE / 'systemd/chatisp-backup-ledger.timer').read_text())

    def test_ledger_reads_deletions_before_maps_and_guards(self):
        result = self.run_script('chatisp-backup.sh', 'ledger')
        self.assertEqual(result.returncode, 0, result.stderr)
        queries = [' '.join(call) for call in self.call_list() if any('\\copy' in arg for arg in call)]
        self.assertEqual(len(queries), 3)
        self.assertIn("select tenant_id, subject, subject_id from deletion_records", queries[0])
        self.assertIn("select id, tenant_id, object_key", queries[1])
        self.assertIn("from privacy_ingress_guards", queries[2])

    def test_failed_audit_does_not_report_new_success(self):
        result = self.run_script('chatisp-backup.sh', 'ledger', DENY_AUDIT='1')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.status.exists())

    def test_empty_first_passphrase_line_is_rejected(self):
        self.pass_file.write_text('\nnot-used-by-openssl\n')
        for script, args, extra in (
            ('chatisp-backup.sh', ('full',), {}),
            ('chatisp-restore.sh', (), {'RESTORE_KEYS_OUT': str(self.root / 'recovered')}),
        ):
            result = self.run_script(script, *args, **extra)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('frase de recuperação vazia', result.stderr)

    def test_check_each_missing_or_late_component_and_installation(self):
        for kind in ('postgres', 'attachments', 'keys', 'ledger'):
            for mode in ('missing', 'late'):
                now = int(__import__('time').time())
                lines = []
                for install in ('fixture-a', 'healthy-b'):
                    for component in ('postgres', 'attachments', 'keys', 'ledger'):
                        if install == 'fixture-a' and component == kind and mode == 'missing':
                            continue
                        stamp = now - 4000 if install == 'fixture-a' and component == kind else now
                        lines.append('chatisp_backup_last_success_timestamp_seconds{installation="%s",kind="%s"} %s' % (install, component, stamp))
                self.status.write_text('\n'.join(lines))
                result = self.run_script('chatisp-backup.sh', 'check', BACKUP_MAX_AGE_SECONDS='3600')
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(kind, result.stderr)
                self.assertNotIn('healthy-b', result.stderr)

    def test_map_uses_full_tenant_attachment_identity_and_rejects_wrong_namespace(self):
        source = (HERE / 'chatisp-restore.sh').read_text()
        programs = [part.split("\n' ", 1)[0] for part in source.split("awk -F'\\t' '\n")[1:]]
        self.assertEqual(len(programs), 2)
        t1, t2 = 'tenant-a', 'tenant-b'
        ns1, ns2 = 'namespace-a', 'namespace-b'
        budgets = self.root / 'budgets'
        budgets.write_text(f'{t1}\t{ns1}\n{t2}\t{ns2}\n')
        rows = self.root / 'rows'
        # An id may be equal in two tenants: association identity includes tenant_id.
        rows.write_text(f'same-id\t{t1}\t{ns1}/object-a\tetag:a\t10\nsame-id\t{t2}\t{ns2}/object-b\tetag:b\t20\n')
        result = subprocess.run(['awk', '-F', '\t', programs[0], str(budgets), str(rows)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        mapping = self.root / 'map'
        mapping.write_text(result.stdout)
        deletions = self.root / 'deletions'
        deletions.write_text(f'{t1}\tsame-id\n')
        result = subprocess.run(['awk', '-F', '\t', programs[1], str(mapping), str(deletions)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), ns1 + '/object-a')
        rows.write_text(f'same-id\t{t1}\t{ns2}/object-b\tetag:b\t20\n')
        result = subprocess.run(['awk', '-F', '\t', programs[0], str(budgets), str(rows)], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('namespace divergente', result.stderr)

    def test_failed_concurrent_restore_never_stops_healthy_application(self):
        result = self.run_script('chatisp-restore.sh', DENY_LOCK='1', RESTORE_KEYS_OUT=str(self.root / 'recovered'))
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any('rm' in call and 'chatisp-api' in call for call in self.call_list()))


if __name__ == '__main__':
    unittest.main()
