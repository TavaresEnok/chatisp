import http.server
import json
import os
import pathlib
import secrets
import ssl
import subprocess
import sys
import tempfile
import threading

root = pathlib.Path(__file__).resolve().parent.parent
os.chdir(root)
if not os.environ.get('K6_BIN'):
    sys.exit('Defina K6_BIN com o caminho do binário k6; o teste não o instala.')
binary = pathlib.Path(os.environ['K6_BIN']).resolve()
if not binary.is_file() or not os.access(binary, os.X_OK):
    sys.exit('K6_BIN não aponta para um executável disponível.')

fail_read = False
invalid_body = False
nonce = secrets.token_hex(32)
tenant = 'a9a04ad8-b078-48b0-9033-f1f67bb79a56'
seen = []


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def respond(self, status, body=b'', headers=None):
        self.send_response(status)
        for key, value in (headers or {}).items():
            self.send_header(key, value)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        seen.append(('GET', self.path))
        if self.path == '/.well-known/chatisp-k6-lab':
            value = nonce if self.headers.get('X-ChatISP-Lab-Nonce') == nonce else 'invalid'
            return self.respond(200, headers={'X-ChatISP-Lab-Nonce': value})
        if self.path == '/api/panel/session' and fail_read:
            return self.respond(503)
        if self.path == '/api/panel/session' and '__Host-session=synthetic' in self.headers.get('Cookie', ''):
            return self.respond(200, json.dumps({'tenant_id': tenant}).encode(), {'Content-Type': 'application/json'})
        self.respond(403)

    def do_POST(self):
        seen.append(('POST', self.path))
        data = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        if self.path != '/api/panel/session' or data['tenant_id'] != tenant:
            return self.respond(403)
        body = json.dumps({
            'tenant_id': tenant, 'csrf_token': 'synthetic-csrf',
            'mfa_enabled': False, 'mfa_enrollment_required': False,
        }).encode()
        if invalid_body:
            body = b'invalid-json'
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Set-Cookie', '__Host-session=synthetic; Secure; HttpOnly; Path=/')
        self.send_header('Set-Cookie', '__Host-csrf=synthetic-csrf; Secure; Path=/')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_DELETE(self):
        seen.append(('DELETE', self.path))
        if self.headers.get('X-CSRF-Token') == 'synthetic-csrf' and '__Host-session=synthetic' in self.headers.get('Cookie', ''):
            return self.respond(204)
        self.respond(403)


with tempfile.TemporaryDirectory(prefix='chatisp-k6-fake-') as directory:
    key = directory + '/key.pem'
    cert = directory + '/cert.pem'
    subprocess.run(
        ['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', key,
         '-out', cert, '-days', '1', '-subj', '/CN=synthetic-lab',
         '-addext', 'subjectAltName=IP:127.0.0.1'],
        check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    server = http.server.HTTPServer(('127.0.0.1', 0), Handler)
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.load_cert_chain(cert, key)
    server.socket = ctx.wrap_socket(server.socket, server_side=True)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    env = {
        key: value for key, value in os.environ.items()
        if key.lower() not in {'http_proxy', 'https_proxy', 'all_proxy', 'no_proxy'}
        and not key.upper().startswith(('K6_', 'CHATISP_K6_', 'SSL_CERT_'))
    }
    os.mkdir(directory + '/empty')
    env.update(
        SSL_CERT_FILE=cert,
        SSL_CERT_DIR=directory + '/empty',
        CHATISP_K6_URL='https://127.0.0.1:' + str(server.server_port),
        CHATISP_K6_NONCE=nonce,
        CHATISP_K6_ATTEST='ambiente-descartavel-e-contas-sinteticas',
        CHATISP_K6_VUS='1',
        CHATISP_K6_ITERACOES_POR_VU='1',
        CHATISP_K6_CONTAS=json.dumps([{
            'email': 'laboratorio@example.test', 'password': 'sintetico', 'tenant_id': tenant,
        }]),
    )
    try:
        result = subprocess.run([str(binary), 'run', '--quiet', 'load/k6-lab.js'],
                                env=env, text=True, capture_output=True, timeout=40)
        print('positive_exit=', result.returncode, 'requests=', seen)
        if result.returncode:
            print(result.stderr[-3000:])
            print(result.stdout[-3000:])
            raise SystemExit(1)
        assert ('POST', '/api/panel/session') in seen and ('DELETE', '/api/panel/session') in seen

        seen.clear()
        fail_read = True
        result = subprocess.run([str(binary), 'run', '--quiet', 'load/k6-lab.js'],
                                env=env, text=True, capture_output=True, timeout=40)
        print('read_failure_exit=', result.returncode, 'requests=', seen)
        assert result.returncode != 0 and ('DELETE', '/api/panel/session') in seen

        fail_read = False
        invalid_body = True
        seen.clear()
        result = subprocess.run([str(binary), 'run', '--quiet', 'load/k6-lab.js'],
                                env=env, text=True, capture_output=True, timeout=40)
        print('invalid_body_exit=', result.returncode, 'requests=', seen)
        assert result.returncode != 0 and ('DELETE', '/api/panel/session') in seen

        invalid_body = False
        seen.clear()
        env['CHATISP_K6_NONCE'] = ('0' if nonce[0] != '0' else '1') + nonce[1:]
        result = subprocess.run([str(binary), 'run', '--quiet', 'load/k6-lab.js'],
                                env=env, text=True, capture_output=True, timeout=40)
        print('bad_nonce_exit=', result.returncode, 'requests=', seen)
        assert result.returncode != 0 and not any(method == 'POST' for method, _ in seen)

        seen.clear()
        env['CHATISP_K6_NONCE'] = nonce
        env['SSL_CERT_FILE'] = directory + '/missing.pem'
        env['SSL_CERT_DIR'] = directory + '/empty'
        result = subprocess.run([str(binary), 'run', '--quiet', 'load/k6-lab.js'],
                                env=env, text=True, capture_output=True, timeout=40)
        print('untrusted_tls_exit=', result.returncode, 'requests=', seen)
        assert result.returncode != 0 and not seen
    finally:
        server.shutdown()
        server.server_close()
