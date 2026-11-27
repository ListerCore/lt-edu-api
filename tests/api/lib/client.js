// Small REST client of lt-edu-api for the test suites
const fs = require('node:fs');
const path = require('node:path');

const API_URL = (process.env.API_URL || 'http://localhost:5050').replace(/\/$/, '');
const SESSIONS_FILE = path.join(__dirname, '..', '.sessions.json');

// Sessions written by the global setup: { "demo-ebr": { director: "<token>", ... }, "demo-eba": { ... } }
function sessions() {
  return JSON.parse(fs.readFileSync(SESSIONS_FILE, 'utf8'));
}

// token of a role in a demo institution; fails loudly when the demo has nobody in that role
function token(slug, role) {
  const value = sessions()[slug]?.[role];
  if (!value) throw new Error(`No hay sesión de ${role} en ${slug}: revisa los datos de la demo`);
  return value;
}

// call does one request and returns { status, body } with the JSON already parsed
async function call(method, route, { token: bearer, body, headers = {} } = {}) {
  const res = await fetch(`${API_URL}${route}`, {
    method,
    headers: {
      ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
      ...(bearer ? { Authorization: `Bearer ${bearer}` } : {}),
      ...headers,
    },
    body: body === undefined ? undefined : typeof body === 'string' ? body : JSON.stringify(body),
  });
  const text = await res.text();
  let parsed = null;
  try {
    parsed = text ? JSON.parse(text) : null;
  } catch {
    parsed = text;
  }
  return { status: res.status, body: parsed };
}

// api binds the institution prefix and a role's session: api('demo-ebr', 'teacher').get('/gradebook')
function api(slug, role) {
  const bearer = role ? token(slug, role) : undefined;
  const base = `/api/v1/institutions/${slug}`;
  return {
    token: bearer,
    get: (route, opts) => call('GET', base + route, { token: bearer, ...opts }),
    post: (route, body, opts) => call('POST', base + route, { token: bearer, body, ...opts }),
    put: (route, body, opts) => call('PUT', base + route, { token: bearer, body, ...opts }),
    patch: (route, body, opts) => call('PATCH', base + route, { token: bearer, body, ...opts }),
    del: (route, opts) => call('DELETE', base + route, { token: bearer, ...opts }),
  };
}

// query builds "?a=1&b=2" skipping empty values
function query(params) {
  const q = new URLSearchParams(Object.entries(params).filter(([, v]) => v !== undefined && v !== ''));
  const s = q.toString();
  return s ? `?${s}` : '';
}

// A DNI that no seeded person has (8 digits starting with 9)
function freshDni() {
  return `9${String(Date.now()).slice(-7)}`;
}

module.exports = { API_URL, SESSIONS_FILE, call, api, token, query, freshDni };
