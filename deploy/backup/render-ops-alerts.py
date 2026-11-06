#!/usr/bin/env python3
"""Optional runtime rules. Thresholds belong to the operator; default emits no rules."""
import json
import os
import re
import sys


def render(env):
    enabled = env.get('CHATISP_OPS_ALERTS_ENABLED', 'false')
    if enabled not in ('true', 'false'):
        raise ValueError('CHATISP_OPS_ALERTS_ENABLED deve ser true ou false')
    if enabled == 'false':
        return {'groups': []}
    installation = env.get('BACKUP_INSTALLATION', '')
    if not re.fullmatch(r'[A-Za-z0-9_.:-]+', installation):
        raise ValueError('defina BACKUP_INSTALLATION único')
    thresholds = {}
    for name in ('OPS_MAX_JOB_AGE_SECONDS', 'OPS_MAX_UNCERTAIN_AGE_SECONDS', 'OPS_MAX_WEBHOOK_AGE_SECONDS'):
        raw = env.get(name, '')
        if not re.fullmatch(r'[1-9][0-9]*', raw):
            raise ValueError('defina ' + name + ' positivo, sem padrão comercial')
        thresholds[name] = int(raw)
    scope = 'installation="' + installation + '"'
    expressions = [
        ('ChatISPColetaFalhou', 'chatisp_ops_collect_ok{' + scope + '} == 0'),
        ('ChatISPWorkerColetaFalhou', 'chatisp_river_collect_ok{' + scope + '} == 0'),
        ('ChatISPServicoIndisponivel', 'up{' + scope + ',job="chatisp-runtime"} == 0'),
        ('ChatISPCanalDegradado', 'chatisp_connectors{' + scope + ',status=~"disconnected|degraded"} > 0'),
        ('ChatISPFilaAntiga', 'chatisp_river_oldest_job_age_seconds{' + scope + '} > ' + str(thresholds['OPS_MAX_JOB_AGE_SECONDS'])),
        ('ChatISPEnvioIncertoAntigo', 'chatisp_outbound_uncertain_oldest_age_seconds{' + scope + '} > ' + str(thresholds['OPS_MAX_UNCERTAIN_AGE_SECONDS'])),
        ('ChatISPWebhookAntigo', 'chatisp_webhook_pending_oldest_age_seconds{' + scope + '} > ' + str(thresholds['OPS_MAX_WEBHOOK_AGE_SECONDS'])),
    ]
    return {'groups': [{'name': 'chatisp-ops-' + installation, 'rules': [
        {'alert': name, 'expr': expression, 'labels': {'severity': 'warning', 'installation': installation},
         'annotations': {'summary': name + ' na instalação ' + installation}}
        for name, expression in expressions
    ]}]}


if __name__ == '__main__':
    try:
        json.dump(render(os.environ), sys.stdout, indent=2)
        sys.stdout.write('\n')
    except ValueError as error:
        sys.exit(str(error))
