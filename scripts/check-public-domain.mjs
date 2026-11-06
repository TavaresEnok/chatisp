#!/usr/bin/env node
// node scripts/check-public-domain.mjs --origin https://example.com --bucket chatisp-anexos
// Opcional: --resolve 10.10.0.10; --email nome@example.com --password-file /caminho/privado
// Usa a confiança TLS padrão do Node, preserva Host/SNI e não segue redirecionamentos.
import http from 'node:http';
import https from 'node:https';
import { readFile } from 'node:fs/promises';
import { randomBytes } from 'node:crypto';
import { isIP } from 'node:net';

const args = process.argv.slice(2);
const opts = {};
for (let i = 0; i < args.length; i += 2) {
  if (!['--origin', '--bucket', '--resolve', '--email', '--password-file'].includes(args[i]) || !args[i + 1] || opts[args[i]]) {
    console.error('Uso: --origin https://dominio --bucket nome [--resolve IP] [--email email --password-file arquivo]');
    process.exit(2);
  }
  opts[args[i]] = args[i + 1];
}
let origin;
try {
  const u = new URL(opts['--origin']);
  if (u.protocol !== 'https:' || u.pathname !== '/' || u.search || u.hash || u.username || u.password) throw new Error();
  origin = u.origin;
  if (!/^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/.test(opts['--bucket'] ?? '')) throw new Error();
  if (opts['--resolve'] && !isIP(opts['--resolve'])) throw new Error();
  if (Boolean(opts['--email']) !== Boolean(opts['--password-file'])) throw new Error();
} catch {
  console.error('Parâmetros inválidos: informe origem HTTPS, bucket e par de credenciais opcional.');
  process.exit(2);
}
const override = opts['--resolve'];
const lookup = override ? (_host, options, cb) => {
  const family = isIP(override);
  if (options.all) cb(null, [{ address: override, family }]);
  else cb(null, override, family);
} : undefined;
const limit = 1024 * 1024;
function request(base, path, { method = 'GET', headers = {}, body } = {}) {
  return new Promise((resolve, reject) => {
    const url = new URL(path, base);
    const payload = body === undefined ? undefined : JSON.stringify(body);
    const transport = url.protocol === 'https:' ? https : http;
    const req = transport.request(url, {
      method, lookup,
      headers: { Origin: origin, ...(payload === undefined ? {} : { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(payload) }), ...headers },
    }, (res) => {
      let length = 0;
      const chunks = [];
      res.on('data', (chunk) => {
        length += chunk.length;
        if (length > limit) { req.destroy(new Error('resposta excedeu limite')); return; }
        chunks.push(chunk);
      });
      res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks).toString('utf8') }));
      res.on('error', reject);
    });
    const timer = setTimeout(() => req.destroy(new Error('tempo esgotado')), 15000);
    req.on('close', () => clearTimeout(timer));
    req.on('error', reject);
    req.on('upgrade', (_res, socket) => { socket.destroy(); reject(new Error('upgrade inesperado')); });
    req.end(payload);
  });
}
function requireStatus(result, expected) {
  if (result.status !== expected) throw new Error(`status ${result.status}; esperado ${expected}`);
}
function json(result) {
  try { return JSON.parse(result.body); } catch { throw new Error('JSON inválido'); }
}
let failures = 0;
async function check(label, fn) {
  try { const result = await fn(); console.log(`${result === 'omitido' ? 'OMITIDO' : 'OK'} ${label}`); return true; }
  catch (error) {
    failures++;
    // Erros de rede recebem somente o código; nunca imprimir corpos, cookies ou credenciais.
    const reason = error.code && /^[A-Z0-9_]+$/.test(error.code) ? error.code : error.message;
    console.log(`FALHOU ${label}: ${reason}`);
    return false;
  }
}
console.log(override ? 'Rota com resolução substituída: não comprova DNS nem acesso externo.' : 'Rota DNS normal: resultado depende da conectividade deste ponto de teste.');
await check('HTTP redireciona para HTTPS', async () => {
  const u = new URL(origin); u.protocol = 'http:'; u.port = '';
  const r = await request(u.origin, '/entrar');
  if (![301, 302, 307, 308].includes(r.status)) throw new Error(`status ${r.status}; esperado redirecionamento`);
  const target = new URL(r.headers.location ?? '', u.origin);
  if (target.origin !== origin || target.pathname !== '/entrar') throw new Error('destino de redirecionamento inesperado');
});
const panelOk = await check('painel HTML e TLS', async () => {
  const r = await request(origin, '/entrar'); requireStatus(r, 200);
  if (!/text\/html/i.test(r.headers['content-type'] ?? '') || !/id=["']root["']/.test(r.body)) throw new Error('HTML do painel ausente');
});
const healthOk = await check('/healthz JSON', async () => { const r = await request(origin, '/healthz'); requireStatus(r, 200); if (json(r).status !== 'ok') throw new Error('estado inesperado'); });
const readyOk = await check('/readyz JSON', async () => { const r = await request(origin, '/readyz'); requireStatus(r, 200); if (json(r).status !== 'ready') throw new Error('estado inesperado'); });
await check('métricas bloqueadas', async () => requireStatus(await request(origin, '/metrics'), 404));
const bucketPath = `/${opts['--bucket']}/`;
const bucketOk = await check('bucket sem assinatura recusado', async () => {
  const r = await request(origin, bucketPath);
  if (r.status !== 403) throw new Error(`status ${r.status}; esperado 403`);
});
if (panelOk && healthOk && readyOk && bucketOk) {
  await check('PUT bloqueado na rota do bucket', async () => requireStatus(await request(origin, `${bucketPath}publicacao-prova-nao-gravar`, { method: 'PUT' }), 404));
} else {
  console.log('OMITIDO PUT: identidade ou proteção do bucket não confirmada.');
}
if (opts['--email'] && failures) {
  console.log('OMITIDO login: verificações passivas não passaram.');
} else if (opts['--email']) {
  let cookie, csrf;
  await check('login e verificações de sessão', async () => {
    const password = (await readFile(opts['--password-file'], 'utf8')).trim();
    const r = await request(origin, '/api/panel/session', { method: 'POST', body: { email: opts['--email'], password } });
    if (r.status === 401 && json(r).detail === 'informe o código de autenticação') {
      console.log('INFO conta exige MFA: desafio respeitado, login e testes autenticados omitidos.');
      return 'omitido';
    }
    requireStatus(r, 200);
    const session = json(r);
    const cookies = r.headers['set-cookie'] ?? [];
    cookie = cookies.map((item) => item.split(';')[0]).join('; ');
    csrf = session.csrf_token;
    if (!cookie || !csrf) throw new Error('sessão sem cookies ou CSRF');
    if (typeof session.mfa_enabled !== 'boolean' || session.mfa_enrollment_required !== false) throw new Error('estado MFA inesperado');
    if (!cookies.some((item) => /^__Host-session=/.test(item) && /;\s*Secure(?:;|$)/i.test(item) && /;\s*HttpOnly(?:;|$)/i.test(item))) throw new Error('cookie de sessão sem atributos esperados');
    if (session.mfa_enabled) throw new Error('login sem desafio para conta com MFA ativo');
    const current = await request(origin, '/api/panel/session', { headers: { Cookie: cookie } });
    requireStatus(current, 200);
    if (json(current).mfa_enabled !== session.mfa_enabled) throw new Error('estado MFA inconsistente');
    requireStatus(await request(origin, '/api/panel/session', { method: 'DELETE', headers: { Cookie: cookie } }), 403);
    requireStatus(await request(origin, '/api/panel/session', { method: 'DELETE', headers: { Cookie: cookie, Origin: 'https://publicacao.invalid', 'X-CSRF-Token': csrf } }), 403);
    requireStatus(await request(origin, '/api/panel/ws', { headers: { Cookie: cookie, Origin: 'https://publicacao.invalid', Connection: 'Upgrade', Upgrade: 'websocket', 'Sec-WebSocket-Version': '13', 'Sec-WebSocket-Key': randomBytes(16).toString('base64') } }), 403);
    console.log('INFO sessão sem MFA ativo: cookie, leitura, CSRF e Origin HTTP/WS conferidos.');
  });
  if (cookie && csrf) await check('logout', async () => requireStatus(await request(origin, '/api/panel/session', { method: 'DELETE', headers: { Cookie: cookie, 'X-CSRF-Token': csrf } }), 204));
}
process.exitCode = failures ? 1 : 0;
