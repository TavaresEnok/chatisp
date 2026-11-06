#!/usr/bin/env python3
"""Actual Alertmanager/amtool test, only synthetic alerts and loopback webhook traffic."""
import http.server
import importlib.util
import json
import os
import pathlib
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request

sys.dont_write_bytecode = True
HERE = pathlib.Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('renderer', HERE / 'render-alertmanager.py')
renderer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(renderer)
IMAGE = 'prom/alertmanager:v0.28.1'
received = []
event = threading.Event()


class Receiver(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        received.append(json.loads(self.rfile.read(int(self.headers['Content-Length']))))
        self.send_response(200)
        self.end_headers()
        event.set()

    def log_message(self, *_):
        pass


def free_port():
    with socket.socket() as connection:
        connection.bind(('127.0.0.1', 0))
        return connection.getsockname()[1]


with tempfile.TemporaryDirectory(prefix='chatisp-alert-delivery-') as folder:
    root = pathlib.Path(folder)
    server = http.server.HTTPServer(('127.0.0.1', 0), Receiver)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        assert renderer.render({})['receivers'] == [{'name': 'disabled'}]
        for env in ({'CHATISP_ALERT_DELIVERY_ENABLED': 'true'},
                    {'CHATISP_ALERT_DELIVERY_ENABLED': 'true', 'CHATISP_ALERT_DESTINATION': 'webhook', 'CHATISP_ALERT_WEBHOOK_URL': 'http://external.invalid/alerts'}):
            try:
                renderer.render(env)
                raise AssertionError('unsafe/incomplete destination accepted')
            except ValueError:
                pass
        configurations = {
            'off': renderer.render({}),
            'smtp': renderer.render({'CHATISP_ALERT_DELIVERY_ENABLED': 'true', 'CHATISP_ALERT_DESTINATION': 'smtp', 'CHATISP_ALERT_SMTP_SMARTHOST': 'smtp.synthetic.invalid:587', 'CHATISP_ALERT_SMTP_FROM': 'chat@synthetic.invalid', 'CHATISP_ALERT_SMTP_TO': 'ops@synthetic.invalid'}),
            'webhook': renderer.render({'CHATISP_ALERT_DELIVERY_ENABLED': 'true', 'CHATISP_ALERT_DESTINATION': 'webhook', 'CHATISP_ALERT_WEBHOOK_URL': 'http://127.0.0.1:%s/alerts' % server.server_port, 'CHATISP_ALERT_TEST_LOOPBACK': '1'}),
        }
        for name, config in configurations.items():
            (root / (name + '.json')).write_text(json.dumps(config))
            subprocess.run(['docker', 'run', '--rm', '--network', 'none', '--user', '0', '-v', f'{root}:/config:ro', '--entrypoint', '/bin/amtool', IMAGE, 'check-config', '/config/' + name + '.json'], check=True)
        port = free_port()
        name = 'chatisp-alert-proof-' + str(os.getpid())
        process = subprocess.Popen(['docker', 'run', '--rm', '--name', name, '--network', 'host', '--user', '0', '-e', 'GOMAXPROCS=2', '-v', f'{root}:/config:ro', '--entrypoint', '/bin/alertmanager', IMAGE, '--config.file=/config/webhook.json', '--storage.path=/tmp/alerts', '--cluster.listen-address=', '--web.listen-address=127.0.0.1:' + str(port)], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        try:
            base = 'http://127.0.0.1:' + str(port)
            for _ in range(60):
                try:
                    urllib.request.urlopen(base + '/-/ready', timeout=1).read()
                    break
                except Exception:
                    time.sleep(0.25)
            else:
                raise AssertionError('Alertmanager did not become ready')
            payload = [{'labels': {'alertname': 'ChatISPBackupSemSinalLedger', 'installation': 'synthetic-proof', 'kind': 'ledger', 'severity': 'warning'}, 'annotations': {'summary': 'synthetic backup alert'}}]
            request = urllib.request.Request(base + '/api/v2/alerts', data=json.dumps(payload).encode(), headers={'Content-Type': 'application/json'}, method='POST')
            urllib.request.urlopen(request, timeout=3).read()
            assert event.wait(20), 'no alert received on the local test receiver'
            labels = received[0]['alerts'][0]['labels']
            assert labels['installation'] == 'synthetic-proof' and labels['kind'] == 'ledger'
            print('Alertmanager: off/SMTP TLS/webhook configs validated; real synthetic webhook delivered locally with installation identity')
        finally:
            subprocess.run(['docker', 'stop', '-t', '3', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
            process.wait(timeout=10)
    finally:
        server.shutdown()
