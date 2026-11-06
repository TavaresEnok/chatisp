#!/usr/bin/env python3
"""Evaluate rules with promtool, including another installation staying healthy."""
import json
import os
import pathlib
import subprocess
import tempfile

HERE = pathlib.Path(__file__).resolve().parent
KINDS = ('postgres', 'attachments', 'keys', 'ledger')
NAMES = dict(zip(KINDS, ('Postgres', 'Attachments', 'Keys', 'Ledger')))
SUMMARIES = {
    'postgres': 'backup do banco (postgres)',
    'attachments': 'espelho de anexos (attachments)',
    'keys': 'cópia de chaves (keys)',
    'ledger': 'exportação do livro de exclusões (ledger)',
}
with tempfile.TemporaryDirectory(prefix='chatisp-promtool-') as folder:
    root = pathlib.Path(folder)
    for installation in ('lab-a', 'lab-b'):
        env = dict(os.environ, BACKUP_INSTALLATION=installation, BACKUP_MAX_AGE_SECONDS='3600')
        result = subprocess.run([str(HERE.parent / 'render-alerts.sh')], env=env, check=True, capture_output=True)
        (root / f'{installation}.yml').write_bytes(result.stdout)
    tests = []
    for mode in ('missing', 'late'):
        for failed in KINDS:
            inputs = []
            for installation in ('lab-a', 'lab-b'):
                for kind in KINDS:
                    if installation == 'lab-a' and kind == failed and mode == 'missing':
                        continue
                    values = '0+0x100' if installation == 'lab-a' and kind == failed else '0+60x100'
                    inputs.append({'series': 'chatisp_backup_last_success_timestamp_seconds{installation="%s",kind="%s"}' % (installation, kind), 'values': values})
            assertions = []
            for kind in KINDS:
                expected = []
                if mode == 'missing' and kind == failed:
                    expected = [{'exp_labels': {'severity': 'warning', 'installation': 'lab-a', 'kind': kind}, 'exp_annotations': {'summary': 'Nenhum sucesso de %s registrado na instalação lab-a' % SUMMARIES[kind]}}]
                assertions.append({'eval_time': '90m', 'alertname': 'ChatISPBackupSemSinal' + NAMES[kind], 'exp_alerts': expected})
            expected = []
            if mode == 'late':
                expected = [{'exp_labels': {'severity': 'warning', 'installation': 'lab-a', 'kind': failed}, 'exp_annotations': {'summary': 'Componente de backup %s da instalação lab-a do Chat ISP mais velho que o limite configurado' % failed}}]
            assertions.append({'eval_time': '90m', 'alertname': 'ChatISPBackupAtrasado', 'exp_alerts': expected})
            tests.append({'name': '%s-%s-other-installation-healthy' % (mode, failed), 'interval': '1m', 'input_series': inputs, 'alert_rule_test': assertions})
    (root / 'tests.yml').write_text(json.dumps({'rule_files': ['lab-a.yml', 'lab-b.yml'], 'evaluation_interval': '1m', 'tests': tests}))
    # Existing local promtool can be supplied; fallback image is a test runner, with no network.
    executable = os.environ.get('PROMTOOL')
    if executable:
        subprocess.run([executable, 'test', 'rules', str(root / 'tests.yml')], cwd=root, check=True)
    else:
        subprocess.run(['docker', 'run', '--rm', '--network', 'none', '--user', '0', '-v', f'{root}:/tests:ro', '-w', '/tests', '--entrypoint', '/bin/promtool', 'prom/prometheus:v3.5.0', 'test', 'rules', 'tests.yml'], check=True)
    print('8 cenários promtool: ausência/atraso de banco, anexos, chaves e ledger; outra instalação saudável não mascara a falha')
