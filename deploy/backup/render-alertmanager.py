#!/usr/bin/env python3
"""Render Alertmanager YAML-compatible JSON. Delivery is explicitly disabled by default.

CHATISP_ALERT_DELIVERY_ENABLED=true requires CHATISP_ALERT_DESTINATION=webhook|smtp.
Webhook: CHATISP_ALERT_WEBHOOK_URL (HTTPS; loopback HTTP only with CHATISP_ALERT_TEST_LOOPBACK=1).
SMTP: CHATISP_ALERT_SMTP_SMARTHOST, CHATISP_ALERT_SMTP_FROM, CHATISP_ALERT_SMTP_TO;
optional CHATISP_ALERT_SMTP_USERNAME and CHATISP_ALERT_SMTP_PASSWORD_FILE (path inside the container).
Redirect stdout to a 0600 config file; never print a configured secret URL in logs.
"""
import json
import os
import sys
import urllib.parse


def required(env, key):
    value = env.get(key, '').strip()
    if not value or any(ord(c) < 32 for c in value):
        raise ValueError('configure ' + key)
    return value


def render(env):
    enabled = env.get('CHATISP_ALERT_DELIVERY_ENABLED', 'false')
    if enabled not in ('false', 'true'):
        raise ValueError('CHATISP_ALERT_DELIVERY_ENABLED deve ser true ou false')
    receiver = {'name': 'disabled'}
    if enabled == 'true':
        receiver = {'name': 'configured'}
        destination = required(env, 'CHATISP_ALERT_DESTINATION')
        if destination == 'webhook':
            url = required(env, 'CHATISP_ALERT_WEBHOOK_URL')
            parsed = urllib.parse.urlsplit(url)
            local_test = (env.get('CHATISP_ALERT_TEST_LOOPBACK') == '1'
                          and parsed.hostname in ('localhost', '127.0.0.1', '::1'))
            if not parsed.hostname or parsed.username or parsed.password or parsed.fragment or (parsed.scheme != 'https' and not (parsed.scheme == 'http' and local_test)):
                raise ValueError('webhook deve usar HTTPS; HTTP somente em teste loopback explícito')
            receiver['webhook_configs'] = [{'url': url, 'send_resolved': True, 'http_config': {'follow_redirects': False}}]
        elif destination == 'smtp':
            config = {
                'smarthost': required(env, 'CHATISP_ALERT_SMTP_SMARTHOST'),
                'from': required(env, 'CHATISP_ALERT_SMTP_FROM'),
                'to': required(env, 'CHATISP_ALERT_SMTP_TO'),
                'require_tls': True,
                'send_resolved': True,
            }
            if env.get('CHATISP_ALERT_SMTP_USERNAME'):
                config['auth_username'] = required(env, 'CHATISP_ALERT_SMTP_USERNAME')
                config['auth_password_file'] = required(env, 'CHATISP_ALERT_SMTP_PASSWORD_FILE')
            receiver['email_configs'] = [config]
        else:
            raise ValueError('CHATISP_ALERT_DESTINATION deve ser webhook ou smtp')
    return {
        'global': {'resolve_timeout': '5m'},
        'route': {'receiver': receiver['name'], 'group_by': ['installation', 'alertname', 'kind'],
                  'group_wait': '5s', 'group_interval': '1m', 'repeat_interval': '4h'},
        'receivers': [receiver],
    }


if __name__ == '__main__':
    try:
        json.dump(render(os.environ), sys.stdout, ensure_ascii=False, indent=2)
        sys.stdout.write('\n')
    except ValueError as error:
        sys.exit(str(error))
