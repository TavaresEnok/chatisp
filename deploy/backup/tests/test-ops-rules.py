#!/usr/bin/env python3
"""Evaluate configured operational rules without any external receiver or service."""
import importlib.util
import json
import pathlib
import subprocess
import tempfile

HERE = pathlib.Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('ops_renderer', HERE / 'render-ops-alerts.py')
renderer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(renderer)
assert renderer.render({}) == {'groups': []}
try:
    renderer.render({'CHATISP_OPS_ALERTS_ENABLED': 'true'})
    raise AssertionError('incomplete configuration accepted')
except ValueError:
    pass
config = renderer.render(dict(CHATISP_OPS_ALERTS_ENABLED='true', BACKUP_INSTALLATION='lab-a',
    OPS_MAX_JOB_AGE_SECONDS='60', OPS_MAX_UNCERTAIN_AGE_SECONDS='60', OPS_MAX_WEBHOOK_AGE_SECONDS='60'))
metrics = [
    ('chatisp_ops_collect_ok', '', 0),
    ('chatisp_river_collect_ok', '', 0),
    ('up', ',job="chatisp-runtime"', 0),
    ('chatisp_connectors', ',status="disconnected"', 1),
    ('chatisp_river_oldest_job_age_seconds', '', 120),
    ('chatisp_outbound_uncertain_oldest_age_seconds', '', 120),
    ('chatisp_webhook_pending_oldest_age_seconds', '', 120),
]
with tempfile.TemporaryDirectory(prefix='chatisp-ops-rules-') as folder:
    root = pathlib.Path(folder)
    (root / 'rules.yml').write_text(json.dumps(config))
    tests = []
    for rule, (metric, extra, value) in zip(config['groups'][0]['rules'], metrics):
        labels = {'alertname': rule['alert'], 'installation': 'lab-a', 'severity': 'warning'}
        if metric == 'up': labels['job'] = 'chatisp-runtime'
        if metric == 'chatisp_connectors': labels['status'] = 'disconnected'
        series = metric + '{installation="lab-a"' + extra + '}'
        other = metric + '{installation="lab-b"' + extra + '}'
        tests.append({'interval': '1m', 'input_series': [{'series': series, 'values': str(value) + '+0x2'},
            {'series': other, 'values': str(value) + '+0x2'}], 'alert_rule_test': [{
            'eval_time': '1m', 'alertname': rule['alert'], 'exp_alerts': [{
                'exp_labels': labels, 'exp_annotations': rule['annotations']}]}]})
    (root / 'tests.yml').write_text(json.dumps({'rule_files': ['rules.yml'], 'evaluation_interval': '1m', 'tests': tests}))
    subprocess.run(['docker', 'run', '--rm', '--network', 'none', '--user', '0', '-v', str(root) + ':/rules:ro',
        '-w', '/rules', '--entrypoint', '/bin/promtool', 'prom/prometheus:v3.5.0', 'test', 'rules', 'tests.yml'], check=True)
print('7 cenários de alertas operacionais avaliados; off/configuração incompleta e isolamento entre instalações conferidos')
