import assert from 'node:assert/strict';
import { labProofValid, parseLabConfig } from './k6-lab-config.mjs';
const env = {
  CHATISP_K6_ATTEST: 'ambiente-descartavel-e-contas-sinteticas',
  CHATISP_K6_NONCE: 'a'.repeat(64), CHATISP_K6_URL: 'https://127.0.0.1:18991', CHATISP_K6_VUS: '1', CHATISP_K6_ITERACOES_POR_VU: '1',
  CHATISP_K6_CONTAS: JSON.stringify([{ email: 'sintetica@example.test', password: 'local', tenant_id: 'a9a04ad8-b078-48b0-9033-f1f67bb79a56' }]),
};
assert.equal(parseLabConfig(env).vus, 1);
assert.equal(labProofValid(200, { 'X-Chatisp-Lab-Nonce': env.CHATISP_K6_NONCE }, env.CHATISP_K6_NONCE), true);
assert.equal(labProofValid(302, { 'X-Chatisp-Lab-Nonce': env.CHATISP_K6_NONCE }, env.CHATISP_K6_NONCE), false);
assert.equal(labProofValid(200, {}, env.CHATISP_K6_NONCE), false);
for (const url of ['https://127.0.0.1:8443', 'https://localhost:18991', 'https://127.0.0.1:18991/path', 'https://127.0.0.1:18991@remote.test', 'https://127.0.0.1:65536', 'http://127.0.0.1:18991', 'https://10.0.0.1:18991']) {
  assert.throws(() => parseLabConfig({ ...env, CHATISP_K6_URL: url }));
}
for (const [key, value] of [['HTTP_PROXY', 'http://remote.test'], ['CHATISP_K6_ATTEST', ''], ['CHATISP_K6_NONCE', 'fake'], ['CHATISP_K6_VUS', '9'], ['CHATISP_K6_VUS', '1.0'], ['CHATISP_K6_ITERACOES_POR_VU', '26'], ['CHATISP_K6_CONTAS', '[]'], ['CHATISP_K6_CONTAS', '{']]) {
  assert.throws(() => parseLabConfig({ ...env, [key]: value }));
}
assert.throws(() => parseLabConfig({ ...env, CHATISP_K6_CONTAS: JSON.stringify([{ email: 'real@example.com', password: 'x', tenant_id: 'a9a04ad8-b078-48b0-9033-f1f67bb79a56' }]) }));
console.log('Guardas locais verificadas; nenhuma requisição efetuada.');
