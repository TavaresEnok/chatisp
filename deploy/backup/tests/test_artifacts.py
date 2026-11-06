#!/usr/bin/env python3
import base64
import hashlib
import importlib.util
import io
import pathlib
import sys
import tarfile
import tempfile
import unittest

sys.dont_write_bytecode = True
HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('artifacts', HERE.parent / 'validate-artifacts.py')
artifacts = importlib.util.module_from_spec(spec)
spec.loader.exec_module(artifacts)


class ArtifactsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)

    def ledger(self, kind, rows='', stamp='20261106T000000.000Z-1234', version=1):
        path = self.root / f'{kind}-{stamp}.tsv'
        body = f'# chatisp-{kind}-ledger-{version}\n{rows}# end-chatisp-{kind}-ledger-{version}\n'.encode()
        path.write_bytes(body)
        pathlib.Path(str(path) + '.sha256').write_text(hashlib.sha256(body).hexdigest() + '\n')
        return path

    def pair(self):
        return self.ledger('attachments'), self.ledger('deletions')

    def test_empty_deletions_are_legitimate(self):
        self.pair()
        artifacts.verify_ledgers(self.root)

    def test_missing_ledger_is_not_empty_deletions(self):
        self.ledger('attachments')
        with self.assertRaisesRegex(ValueError, 'obrigatório ausente'):
            artifacts.verify_ledgers(self.root)

    def test_corrupt_even_when_delimiters_survive(self):
        self.pair()
        path = next(self.root.glob('deletions-*.tsv'))
        path.write_bytes(path.read_bytes().replace(b'deletions', b'deletioNs'))
        with self.assertRaisesRegex(ValueError, 'corrompido'):
            artifacts.verify_ledgers(self.root)

    def test_missing_checksum(self):
        self.pair()
        next(self.root.glob('deletions-*.sha256')).unlink()
        with self.assertRaisesRegex(ValueError, 'checksum ausente'):
            artifacts.verify_ledgers(self.root)

    def test_partial_publication_is_refused(self):
        self.pair()
        (self.root / '.deletions-interrupted.partial').write_bytes(b'partial')
        with self.assertRaisesRegex(ValueError, 'parciais'):
            artifacts.verify_ledgers(self.root)

    def test_unpaired_export_is_refused(self):
        self.pair()
        self.ledger('attachments', stamp='later')
        with self.assertRaisesRegex(ValueError, 'não formam pares'):
            artifacts.verify_ledgers(self.root)

    def test_guarded_export_requires_valid_guard_and_original_cutoff(self):
        self.ledger('attachments')
        self.ledger('deletions', version=2)
        with self.assertRaisesRegex(ValueError, 'guards não formam pares'):
            artifacts.verify_ledgers(self.root)
        tenant = '11111111-1111-1111-1111-111111111111'
        connector = '22222222-2222-2222-2222-222222222222'
        row = f'{tenant}\t{connector}\told-2\t{"ab" * 32}\t2026-11-06T02:50:00.123456Z\n'
        guard = self.ledger('guards', row)
        artifacts.verify_ledgers(self.root)
        guard.unlink()
        pathlib.Path(str(guard) + '.sha256').unlink()
        self.ledger('guards', row.replace('2026-11-06', '2026-99-06'))
        with self.assertRaisesRegex(ValueError, 'malformada'):
            artifacts.verify_ledgers(self.root)

    def test_legacy_contact_fails_closed_and_guarded_requires_v2(self):
        tenant = '11111111-1111-1111-1111-111111111111'
        contact = '22222222-2222-2222-2222-222222222222'
        self.ledger('attachments')
        row = f'{tenant}\tcontact_privacy\t{contact}\n'
        path = self.ledger('deletions', row)
        with self.assertRaisesRegex(ValueError, 'exclusão histórica sem prova'):
            artifacts.verify_ledgers(self.root)
        path.unlink()
        pathlib.Path(str(path) + '.sha256').unlink()
        self.ledger('deletions', row.replace('contact_privacy', 'contact_privacy_guarded'))
        with self.assertRaisesRegex(ValueError, 'malformada'):
            artifacts.verify_ledgers(self.root)
        self.ledger('deletions', row.replace('contact_privacy', 'contact_privacy_guarded'), version=2)
        self.ledger('guards')
        artifacts.verify_ledgers(self.root)

    def test_bad_uuid_is_refused_even_with_valid_checksum(self):
        self.ledger('attachments')
        self.ledger('deletions', 'invalid\tattachment\tinvalid\n')
        with self.assertRaisesRegex(ValueError, 'malformada'):
            artifacts.verify_ledgers(self.root)

    def test_tar_rejects_symlinks_and_traversal(self):
        for name, kind in [('ledgers/../../outside', tarfile.REGTYPE), ('ledgers/evil', tarfile.SYMTYPE), ('ledgers/.export-ab.inflight', tarfile.REGTYPE)]:
            archive = self.root / 'bad.tar'
            with tarfile.open(archive, 'w') as out:
                member = tarfile.TarInfo(name)
                member.type = kind
                member.linkname = '/etc/passwd'
                out.addfile(member, io.BytesIO())
            with self.assertRaisesRegex(ValueError, 'inesperado'):
                artifacts.extract_ledgers(archive, self.root / 'out')

    def test_key_format_matches_application(self):
        path = self.root / 'keys'
        key = base64.b64encode(bytes(range(32))).decode()
        for version in ['1', 'old-2']:
            path.write_text(f'CHATISP_DATA_KEYS={version}:{key}\nCHATISP_DATA_KEY_VERSION={version}\n')
            artifacts.verify_keys(path)

    def test_reference_coverage_refuses_missing_or_different_object(self):
        manifest = self.root / 'objects.manifest'
        refs = self.root / 'refs'
        key = '11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222'
        digest = 'a' * 64
        def write_manifest(rows):
            body = "".join(row + "\n" for row in rows)
            footer = "# end-attachments-mirror-1 entries=%d sha256=%s\n" % (len(rows), hashlib.sha256(body.encode()).hexdigest())
            manifest.write_text('# attachments-mirror-1 key<TAB>version<TAB>sha256<TAB>bytes\n' + body + footer)
        write_manifest([])
        refs.write_text(f'{key}\tetag:123\t{digest}\t100\tvalidated\n')
        with self.assertRaisesRegex(ValueError, 'não cobre'):
            artifacts.verify_references(manifest, refs)
        write_manifest([f'{key}\tetag:123\t{digest}\t100'])
        artifacts.verify_references(manifest, refs)
        for field, replacement in [(1, 'etag:other'), (2, 'b' * 64), (3, '200')]:
            fields = [key, 'etag:123', digest, '100', 'validated']
            fields[field] = replacement
            refs.write_text('\t'.join(fields) + '\n')
            with self.assertRaisesRegex(ValueError, 'não cobre'):
                artifacts.verify_references(manifest, refs)

    def test_missing_active_short_or_duplicate_key_refused(self):
        path = self.root / 'keys'
        key = base64.b64encode(bytes(range(32))).decode()
        for ring, current in [(f'1:{key}', '2'), ('1:YQ==', '1'), (f'1:{key},1:{key}', '1')]:
            path.write_text(f'CHATISP_DATA_KEYS={ring}\nCHATISP_DATA_KEY_VERSION={current}\n')
            with self.assertRaises(ValueError):
                artifacts.verify_keys(path)


if __name__ == '__main__':
    unittest.main()
