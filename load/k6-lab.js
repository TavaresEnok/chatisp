import http from 'k6/http';
import { check, fail } from 'k6';
import { labProofValid, parseLabConfig } from './k6-lab-config.mjs';

const cfg = parseLabConfig(__ENV);
// Limits below bound this synthetic tool, not the capacity of an installation.
export const options = {
  scenarios: { laboratorio: { executor: 'per-vu-iterations', vus: cfg.vus, iterations: cfg.iterations, maxDuration: '5m' } },
  insecureSkipTLSVerify: false,
  thresholds: { checks: ['rate==1'], iterations: [`count==${cfg.vus * cfg.iterations}`] },
  maxRedirects: 0,
};

// Require a disposable lab proxy that explicitly recognizes this run before
// sending any credentials. The normal application has no such endpoint.
function verifyLab() {
  const proof = http.get(`${cfg.url}/.well-known/chatisp-k6-lab`, {
    headers: { 'X-ChatISP-Lab-Nonce': cfg.nonce }, redirects: 0, timeout: '3s', tags: { name: 'prova_laboratorio' },
  });
  if (!check(proof, { 'proxy descartável reconheceu o nonce': r => labProofValid(r.status, r.headers, cfg.nonce) })) {
    fail('endpoint descartável não reconheceu o nonce; nenhuma credencial enviada');
  }
}

export function setup() { verifyLab(); }

export default function () {
  let iterationOK = false;
  try {
    verifyLab();
    const account = cfg.accounts[__VU - 1];
    const origin = cfg.url;
    const params = { headers: { 'Content-Type': 'application/json', Origin: origin }, redirects: 0, timeout: '10s', tags: { name: 'login_sintetico' } };
    const login = http.post(`${origin}/api/panel/session`, JSON.stringify(account), params);
    if (!check(login, { 'login sintético aceito': r => r.status === 200 })) fail('login sintético falhou');
    let csrf = '';
    let operationOK = false;
    try {
      // The readable CSRF cookie also permits logout if the response body is invalid.
      const cookies = http.cookieJar().cookiesForURL(origin);
      csrf = (cookies['__Host-csrf'] || [])[0] || '';
      let session;
      try { session = login.json(); } catch (_) { fail('resposta de login inválida'); }
      if (!session || session.tenant_id !== account.tenant_id || !session.csrf_token || session.mfa_enabled || session.mfa_enrollment_required) {
        fail('sessão não corresponde à conta sintética sem MFA');
      }
      csrf = session.csrf_token;
      const current = http.get(`${origin}/api/panel/session`, { redirects: 0, timeout: '10s', tags: { name: 'ler_sessao' } });
      if (!check(current, { 'sessão lida': r => r.status === 200 && r.json('tenant_id') === account.tenant_id })) {
        fail('leitura de sessão sintética falhou');
      }
      operationOK = true;
    } finally {
      check(operationOK, { 'jornada autenticada concluída': ok => ok });
      const logout = http.del(`${origin}/api/panel/session`, null, {
        headers: { Origin: origin, 'X-CSRF-Token': csrf }, redirects: 0, timeout: '10s', tags: { name: 'logout_sintetico' },
      });
      if (!check(logout, { 'logout concluído': r => r.status === 204 })) fail('logout sintético falhou');
    }
    iterationOK = true;
  } finally {
    check(iterationOK, { 'iteração concluída': ok => ok });
  }
}
