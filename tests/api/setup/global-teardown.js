// Removes the sessions and accounts that the global setup opened (cmd/demosessions -cleanup). The accounts that
// left audit events cannot be deleted (the audit log is append-only), so a failed cleanup only warns: the
// sessions expire on their own
const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { SESSIONS_FILE } = require('../lib/client');

module.exports = async () => {
  fs.rmSync(SESSIONS_FILE, { force: true });
  if (process.env.LT_EDU_SESSIONS_CREATED !== '1' || process.env.KEEP_SESSIONS === '1') return;
  try {
    execFileSync('go', ['run', './cmd/demosessions', '-cleanup'], { cwd: path.join(__dirname, '..', '..', '..'), stdio: 'ignore' });
  } catch {
    console.warn('demosessions -cleanup no pudo borrar todas las cuentas de prueba (auditoría de solo inserción); las sesiones vencen solas.');
  }
};
