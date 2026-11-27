// Opens one session per role in two demo institutions with cmd/demosessions (it refuses production and any slug
// that does not start with "demo-") and leaves them in .sessions.json for the suites.
// SESSIONS_EBR / SESSIONS_EBA can point to files already made by demosessions to skip this step.
const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { API_URL, SESSIONS_FILE } = require('../lib/client');

const API_ROOT = path.join(__dirname, '..', '..', '..');

function open(slug, file) {
  if (file) return JSON.parse(fs.readFileSync(file, 'utf8'));
  const out = execFileSync('go', ['run', './cmd/demosessions', '-slug', slug], { cwd: API_ROOT, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] });
  return JSON.parse(out);
}

module.exports = async () => {
  const health = await fetch(`${API_URL}/api/v1/health`).catch(() => null);
  if (!health || !health.ok) {
    throw new Error(`lt-edu-api no responde en ${API_URL}: levántala (make dev) o define API_URL`);
  }
  const sessions = {
    'demo-ebr': open('demo-ebr', process.env.SESSIONS_EBR),
    'demo-eba': open('demo-eba', process.env.SESSIONS_EBA),
  };
  fs.writeFileSync(SESSIONS_FILE, JSON.stringify(sessions, null, 2));
  process.env.LT_EDU_SESSIONS_CREATED = process.env.SESSIONS_EBR ? '' : '1';
};
