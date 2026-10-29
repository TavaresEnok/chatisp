import http from "node:http";

export type SeenRequest = {
  method: string;
  path: string;
  body: unknown;
  csrf: string | null;
  cookie: string | null;
};

type Account = {
  email: string;
  password: string;
  role: "admin" | "supervisor" | "agent";
  mfa: "none" | "pending" | "active";
  recoveryCode: string;
  superadmin: boolean;
  userId: string;
  membershipId: string;
  sessionId: string;
  csrf: string;
  cookie: string;
};

type Membership = {
  id: string;
  user_id: string;
  role: "admin" | "supervisor" | "agent";
  status: "invited" | "active" | "disabled";
  created_at: string;
};

type Tenant = {
  id: string;
  timezone: string;
  status: "active" | "suspended";
  limits: Record<string, never>;
  created_at: string;
  updated_at: string;
};

type Issued = {
  user_id: string;
  email: string;
  tenant_id: string;
  membership_id: string;
  role: string;
  session_id: string;
  csrf_token: string;
  expires_at: string;
  mfa_enrollment_required: boolean;
};

const createdAt = "2026-10-01T12:00:00.000Z";
const expiresAt = "2026-12-01T12:00:00.000Z";

function problem(res: http.ServerResponse, status: number, detail: string) {
  res.writeHead(status, { "content-type": "application/problem+json; charset=utf-8" });
  res.end(
    JSON.stringify({
      type: "urn:chatisp:problem:test",
      title: status === 401 ? "Não autenticado" : "Acesso negado",
      status,
      detail,
    }),
  );
}

function sendJson(res: http.ServerResponse, status: number, body: unknown, cookies?: string[]) {
  const headers: http.OutgoingHttpHeaders = {
    "content-type": "application/json; charset=utf-8",
  };
  if (cookies) {
    headers["set-cookie"] = cookies;
  }
  res.writeHead(status, headers);
  res.end(JSON.stringify(body));
}

function sendEmpty(res: http.ServerResponse, status: number, cookies?: string[]) {
  const headers: http.OutgoingHttpHeaders = {};
  if (cookies) {
    headers["set-cookie"] = cookies;
  }
  res.writeHead(status, headers);
  res.end();
}

function headerValue(value: string | string[] | undefined): string | null {
  if (Array.isArray(value)) {
    return value[0] ?? null;
  }
  return value ?? null;
}

export class PanelDouble {
  requests: SeenRequest[] = [];
  cookie = "sessao-opaca";
  csrf = "csrf-publico";
  revoked = false;
  issued: Issued | null = null;
  membershipStatus: number | null = null;
  accounts: Account[] = [
    {
      email: "ana@provedor.test",
      password: "senha-certa",
      role: "admin",
      mfa: "none",
      recoveryCode: "rec-123456",
      superadmin: false,
      userId: "11111111-1111-4111-8111-111111111111",
      membershipId: "33333333-3333-4333-8333-333333333333",
      sessionId: "44444444-4444-4444-8444-444444444444",
      csrf: "csrf-publico",
      cookie: "sessao-opaca",
    },
    {
      email: "bia@provedor.test",
      password: "senha-certa",
      role: "agent",
      mfa: "none",
      recoveryCode: "rec-654321",
      superadmin: false,
      userId: "77777777-7777-4777-8777-777777777777",
      membershipId: "88888888-8888-4888-8888-888888888888",
      sessionId: "99999999-9999-4999-8999-999999999999",
      csrf: "csrf-bia",
      cookie: "sessao-bia",
    },
  ];
  memberships: Membership[] = [
    {
      id: "33333333-3333-4333-8333-333333333333",
      user_id: "11111111-1111-4111-8111-111111111111",
      role: "admin",
      status: "active",
      created_at: createdAt,
    },
  ];
  tenants: Tenant[] = [];
  private inviteCount = 0;

  handle(req: http.IncomingMessage, res: http.ServerResponse, body: unknown) {
    const url = new URL(req.url ?? "/", "http://127.0.0.1");
    const method = req.method ?? "GET";
    const path = url.pathname;
    this.requests.push({
      method,
      path,
      body,
      csrf: headerValue(req.headers["x-csrf-token"]),
      cookie: headerValue(req.headers.cookie),
    });

    if (method === "POST" && path === "/api/panel/session") {
      this.login(res, body);
      return;
    }
    if (method === "POST" && path === "/api/panel/invitations/accept") {
      this.accept(res, body);
      return;
    }
    if (method === "POST" && path === "/api/panel/password-resets") {
      sendJson(res, 202, { status: "accepted" });
      return;
    }
    if (method === "POST" && path === "/api/panel/password-resets/confirm") {
      this.confirmReset(res, body);
      return;
    }
    if (method === "GET" && path === "/api/panel/session") {
      if (!this.authenticated(req)) {
        problem(res, 401, "não autenticado");
        return;
      }
      sendJson(res, 200, this.principal());
      return;
    }
    if (method === "DELETE" && path === "/api/panel/session") {
      if (this.authenticated(req) && !this.validCsrf(req)) {
        problem(res, 403, "token de CSRF ausente ou inválido");
        return;
      }
      this.revoked = true;
      this.issued = null;
      sendEmpty(res, 204, this.clearCookies());
      return;
    }
    if (!this.authenticated(req)) {
      problem(res, 401, "não autenticado");
      return;
    }
    if (method !== "GET" && !this.validCsrf(req)) {
      problem(res, 403, "token de CSRF ausente ou inválido");
      return;
    }
    if (this.issued?.mfa_enrollment_required && path !== "/api/panel/mfa/enrollment" && path !== "/api/panel/mfa/enrollment/confirm") {
      problem(res, 403, "conclua o cadastro do MFA antes de acessar o painel");
      return;
    }

    if (method === "POST" && path === "/api/panel/mfa/enrollment") {
      sendJson(res, 200, {
        otpauth_url: "otpauth://totp/Chat%20ISP:ana@provedor.test?secret=SEGREDOBASE32",
        secret: "SEGREDOBASE32",
        recovery_codes: ["codigo-um", "codigo-dois"],
      });
      return;
    }
    if (method === "POST" && path === "/api/panel/mfa/enrollment/confirm") {
      const code = this.record(body).code;
      if (code !== "123456") {
        problem(res, 401, "código de autenticação inválido");
        return;
      }
      if (this.issued) {
        this.issued = { ...this.issued, mfa_enrollment_required: false };
      }
      sendJson(res, 200, { mfa_enrollment_required: false });
      return;
    }
    if (method === "GET" && path === "/api/panel/memberships") {
      if (this.membershipStatus !== null) {
        problem(res, this.membershipStatus, "acesso negado");
        return;
      }
      if (this.issued?.role !== "admin") {
        problem(res, 403, "acesso negado");
        return;
      }
      sendJson(res, 200, { memberships: this.memberships });
      return;
    }
    if (method === "POST" && path === "/api/panel/invitations") {
      if (this.issued?.role !== "admin") {
        problem(res, 403, "acesso negado");
        return;
      }
      this.inviteCount += 1;
      const input = this.record(body);
      this.memberships.push({
        id: `44444444-4444-4444-8444-44444444444${this.inviteCount}`,
        user_id: `55555555-5555-4555-8555-55555555555${this.inviteCount}`,
        role: input.role === "admin" || input.role === "supervisor" || input.role === "agent" ? input.role : "agent",
        status: "invited",
        created_at: createdAt,
      });
      sendJson(res, 201, { expires_at: expiresAt });
      return;
    }
    const membership = path.match(/^\/api\/panel\/memberships\/([^/]+)\/(deactivate|reactivate)$/);
    if (method === "POST" && membership) {
      const target = this.memberships.find((item) => item.id === membership[1]);
      if (!target) {
        problem(res, 404, "vínculo não encontrado");
        return;
      }
      target.status = membership[2] === "deactivate" ? "disabled" : "active";
      sendEmpty(res, 204);
      return;
    }
    if (method === "GET" && path === "/api/panel/sessions") {
      if (this.issued?.role !== "admin") {
        problem(res, 403, "acesso negado");
        return;
      }
      sendJson(res, 200, {
        sessions: [
          {
            id: this.issued?.session_id,
            user_id: this.issued?.user_id,
            created_at: createdAt,
            expires_at: expiresAt,
            current: true,
          },
        ],
      });
      return;
    }
    const revoke = path.match(/^\/api\/panel\/sessions\/([^/]+)\/revoke$/);
    if (method === "POST" && revoke) {
      if (revoke[1] === this.issued?.session_id) {
        this.revoked = true;
      }
      sendEmpty(res, 204);
      return;
    }
    if (method === "GET" && path === "/api/platform/tenants") {
      if (!this.accounts.some((account) => account.email === this.issued?.email && account.superadmin)) {
        problem(res, 403, "acesso negado");
        return;
      }
      sendJson(res, 200, { tenants: this.tenants });
      return;
    }
    if (method === "POST" && path === "/api/platform/tenants") {
      if (!this.accounts.some((account) => account.email === this.issued?.email && account.superadmin)) {
        problem(res, 403, "acesso negado");
        return;
      }
      const input = this.record(body);
      const tenant: Tenant = {
        id: "66666666-6666-4666-8666-666666666666",
        timezone: typeof input.timezone === "string" ? input.timezone : "America/Sao_Paulo",
        status: "active",
        limits: {},
        created_at: createdAt,
        updated_at: createdAt,
      };
      this.tenants.push(tenant);
      sendJson(res, 201, tenant);
      return;
    }
    const tenantAction = path.match(/^\/api\/platform\/tenants\/([^/]+)\/(suspend|reactivate)$/);
    if (method === "POST" && tenantAction) {
      const tenant = this.tenants.find((item) => item.id === tenantAction[1]);
      if (!tenant) {
        problem(res, 404, "provedor não encontrado");
        return;
      }
      tenant.status = tenantAction[2] === "suspend" ? "suspended" : "active";
      tenant.updated_at = createdAt;
      sendJson(res, 200, tenant);
      return;
    }
    problem(res, 404, "o caminho solicitado não existe");
  }

  private login(res: http.ServerResponse, body: unknown) {
    const input = this.record(body);
    const account = this.accounts.find((item) => item.email === input.email);
    if (!account || account.password !== input.password) {
      problem(res, 401, "credenciais inválidas");
      return;
    }
    if (account.mfa === "active" && input.code !== account.recoveryCode && input.code !== "123456") {
      problem(res, 401, input.code ? "código de autenticação inválido" : "informe o código de autenticação");
      return;
    }
    this.issue(res, account, account.mfa === "pending");
  }

  private accept(res: http.ServerResponse, body: unknown) {
    const input = this.record(body);
    if (input.token !== "convite-ok") {
      problem(res, 400, "token inválido ou expirado");
      return;
    }
    const account = this.accounts[0];
    if (!account) {
      problem(res, 400, "token inválido ou expirado");
      return;
    }
    this.issue(res, account, false);
  }

  private confirmReset(res: http.ServerResponse, body: unknown) {
    const input = this.record(body);
    if (input.token !== "token-ok") {
      problem(res, 400, "token inválido ou expirado");
      return;
    }
    sendEmpty(res, 204);
  }

  private issue(res: http.ServerResponse, account: Account, enrollment: boolean) {
    this.revoked = false;
    this.cookie = account.cookie;
    this.csrf = account.csrf;
    this.issued = {
      user_id: account.userId,
      email: account.email,
      tenant_id: "22222222-2222-4222-8222-222222222222",
      membership_id: account.membershipId,
      role: account.role,
      session_id: account.sessionId,
      csrf_token: account.csrf,
      expires_at: expiresAt,
      mfa_enrollment_required: enrollment,
    };
    this.memberships[0] = {
      id: this.issued.membership_id,
      user_id: this.issued.user_id,
      role: account.role,
      status: "active",
      created_at: createdAt,
    };
    sendJson(res, 200, this.issued, this.sessionCookies());
  }

  private principal() {
    return {
      user_id: this.issued?.user_id,
      email: this.issued?.email,
      tenant_id: this.issued?.tenant_id,
      membership_id: this.issued?.membership_id,
      role: this.issued?.role,
      session_id: this.issued?.session_id,
      expires_at: this.issued?.expires_at,
      mfa_enrollment_required: this.issued?.mfa_enrollment_required ?? false,
    };
  }

  private authenticated(req: http.IncomingMessage): boolean {
    const cookie = headerValue(req.headers.cookie) ?? "";
    return !this.revoked && this.issued !== null && cookie.includes(`${"__Host-session"}=${this.cookie}`);
  }

  private validCsrf(req: http.IncomingMessage): boolean {
    return headerValue(req.headers["x-csrf-token"]) === this.csrf;
  }

  private sessionCookies(): string[] {
    return [
      `__Host-session=${this.cookie}; Path=/; Secure; HttpOnly; SameSite=Lax`,
      `__Host-csrf=${this.csrf}; Path=/; Secure; SameSite=Lax`,
    ];
  }

  private clearCookies(): string[] {
    return [
      "__Host-session=; Path=/; Secure; HttpOnly; SameSite=Lax; Max-Age=0",
      "__Host-csrf=; Path=/; Secure; SameSite=Lax; Max-Age=0",
    ];
  }

  private record(body: unknown): Record<string, string> {
    if (typeof body !== "object" || body === null) {
      return {};
    }
    const output: Record<string, string> = {};
    for (const [key, value] of Object.entries(body)) {
      if (typeof value === "string") {
        output[key] = value;
      }
    }
    return output;
  }
}

function rememberCookie(jar: Map<string, string>, raw: string) {
  const [pair, ...attributes] = raw.split(";").map((part) => part.trim());
  if (!pair) {
    return;
  }
  const separator = pair.indexOf("=");
  if (separator < 0) {
    return;
  }
  const name = pair.slice(0, separator);
  const value = pair.slice(separator + 1);
  const expired = attributes.some((attribute) => /^max-age=(0|-1)$/i.test(attribute));
  if (expired || value.length === 0) {
    jar.delete(name);
    return;
  }
  jar.set(name, value);
}

function setCookiesOf(response: Response): string[] {
  const withList = response.headers as Headers & { getSetCookie?: () => string[] };
  const listed = withList.getSetCookie?.() ?? [];
  if (listed.length > 0) {
    return listed;
  }
  const single = response.headers.get("set-cookie");
  return single ? [single] : [];
}

export async function startPanelDouble(double: PanelDouble) {
  const server = http.createServer(async (req, res) => {
    const chunks: Buffer[] = [];
    for await (const chunk of req) {
      chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
    }
    const raw = Buffer.concat(chunks).toString("utf8");
    let body: unknown = null;
    if (raw.length > 0) {
      try {
        body = JSON.parse(raw) as unknown;
      } catch {
        body = null;
      }
    }
    double.handle(req, res, body);
  });
  await new Promise<void>((resolve) => {
    server.listen(0, "127.0.0.1", () => {
      resolve();
    });
  });
  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("porta ausente");
  }
  return {
    origin: `http://127.0.0.1:${address.port}`,
    close: () =>
      new Promise<void>((resolve, reject) => {
        server.close((error) => {
          if (error) {
            reject(error);
            return;
          }
          resolve();
        });
      }),
  };
}

export async function installPanelOrigin(origin: string, run: () => Promise<void>) {
  const jar = new Map<string, string>();
  const previous = globalThis.fetch;
  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = input instanceof Request ? input : new Request(input, init);
    const incoming = new URL(request.url);
    const target = new URL(`${incoming.pathname}${incoming.search}`, origin);
    const headers = new Headers(request.headers);
    headers.delete("host");
    const baked = [...jar.entries()].map(([name, value]) => `${name}=${value}`).join("; ");
    if (baked.length > 0) {
      headers.set("cookie", baked);
    }
    const method = request.method.toUpperCase();
    const body = method === "GET" || method === "HEAD" ? undefined : await request.arrayBuffer();
    const response = await previous(target, { method, headers, body });
    for (const raw of setCookiesOf(response)) {
      rememberCookie(jar, raw);
    }
    return response;
  };
  try {
    await run();
  } finally {
    globalThis.fetch = previous;
  }
}
