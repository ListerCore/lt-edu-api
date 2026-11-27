// With CLEANUP_SESSIONS=1, removes the demo sessions and accounts (cmd/demosessions -cleanup). It is opt-in because
// the cleanup revokes every walkthrough session, also the ones a k6 run is using. The accounts that left audit
// events cannot be deleted (the audit log is append-only), so a failed cleanup only warns
const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { SESSIONS_FILE } = require('../lib/client');

module.exports = async () => {
  fs.rmSync(SESSIONS_FILE, { force: true });
  if (process.env.LT_EDU_SESSIONS_CREATED !== '1' || process.env.CLEANUP_SESSIONS !== '1') return;
  try {
    execFileSync('go', ['run', './cmd/demosessions', '-cleanup'], { cwd: path.join(__dirname, '..', '..', '..'), stdio: 'ignore' });
  } catch {
    console.warn('demosessions -cleanup no pudo borrar todas las cuentas de prueba (auditoría de solo inserción); las sesiones vencen solas.');
  }
};
