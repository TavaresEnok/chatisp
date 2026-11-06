// Parsing and guardrails shared by k6 and offline tests. No network access.
export function parseLabConfig(env) {
  for (const name of ['HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'http_proxy', 'https_proxy', 'all_proxy']) {
    if (env[name]) throw new Error('proxy de ambiente não é permitido no ensaio local');
  }
  if (env.CHATISP_K6_ATTEST !== 'ambiente-descartavel-e-contas-sinteticas') {
    throw new Error('declare ambiente descartável e contas sintéticas');
  }
  // Only literal IPv4 loopback. Disallow paths, userinfo, DNS and the active installation's port.
  const match = /^https:\/\/127\.0\.0\.1:([1-9][0-9]{0,4})$/.exec(env.CHATISP_K6_URL || '');
  if (!match || Number(match[1]) > 65535 || Number(match[1]) === 8443) {
    throw new Error('alvo deve ser HTTPS em 127.0.0.1 com porta explícita de laboratório, exceto 8443');
  }
  if (!/^[a-f0-9]{64}$/.test(env.CHATISP_K6_NONCE || '')) throw new Error('nonce de laboratório inválido');
  function integer(name, max) {
    const value = env[name] || '';
    if (!/^[1-9][0-9]*$/.test(value) || Number(value) > max) throw new Error(`${name} inválido`);
    return Number(value);
  }
  const vus = integer('CHATISP_K6_VUS', 8);
  const iterations = integer('CHATISP_K6_ITERACOES_POR_VU', 25);
  let accounts;
  try { accounts = JSON.parse(env.CHATISP_K6_CONTAS || ''); } catch (_) { throw new Error('contas sintéticas inválidas'); }
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
  if (!Array.isArray(accounts) || accounts.length !== vus || accounts.some(a =>
    !a || typeof a.email !== 'string' || !/^[^\s@]+@[^\s@]+\.test$/i.test(a.email) ||
    typeof a.password !== 'string' || a.password.length < 1 || !uuid.test(a.tenant_id || ''))) {
    throw new Error('informe uma conta .test e tenant sintético por VU');
  }
  if (new Set(accounts.map(a => a.email.toLowerCase())).size !== vus) throw new Error('contas devem ser distintas');
  accounts = accounts.map(a => ({ email: a.email, password: a.password, tenant_id: a.tenant_id.toLowerCase() }));
  return { url: env.CHATISP_K6_URL, nonce: env.CHATISP_K6_NONCE, vus, iterations, accounts };
}

export function labProofValid(status, headers, nonce) {
  return status === 200 && headers && headers['X-Chatisp-Lab-Nonce'] === nonce;
}
