// EQ-04 · AC04 Disponibilidad: carga constante sobre los módulos críticos (contexto, asistencia, notas,
// estudiantes) mientras se detiene un componente no crítico (LiveKit). Meta: 0 errores en los módulos críticos.
// eq04-disponibilidad.sh stops the component in the middle of this run.
import http from 'k6/http';
import { check, sleep } from 'k6';

const API = (__ENV.API_URL || 'http://localhost:5050').replace(/\/$/, '');
const SESSIONS = JSON.parse(open(__ENV.SESSIONS || './sessions.json'));
const base = `${API}/api/v1/institutions/demo-ebr`;
const auth = (token) => ({ headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' } });

export const options = {
  scenarios: { criticos: { executor: 'constant-vus', vus: Number(__ENV.VUS || 100), duration: __ENV.DURATION || '90s' } },
  thresholds: { http_req_failed: ['rate<0.001'], checks: ['rate>0.999'], http_req_duration: ['p(95)<500'] },
  summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
};

export function setup() {
  const book = http.get(`${base}/gradebook`, auth(SESSIONS['demo-ebr'].teacher)).json();
  return { sectionId: book.sheets[0].section_id };
}

export default function (data) {
  const s = SESSIONS['demo-ebr'];
  const calls = [
    ['contexto', () => http.get(`${base}/context`, auth(s.director))],
    ['lista_seccion', () => http.get(`${base}/sections/${data.sectionId}/attendance`, auth(s.teacher))],
    ['registro_notas', () => http.get(`${base}/gradebook`, auth(s.teacher))],
    ['estudiantes', () => http.get(`${base}/students?limit=20`, auth(s.secretary))],
  ];
  const [name, run] = calls[Math.floor(Math.random() * calls.length)];
  const res = run();
  check(res, { [`${name} 200`]: (r) => r.status === 200 });
  sleep(0.5 + Math.random());
}

export function handleSummary(data) {
  const m = data.metrics;
  const text = [
    `EQ-04 · ${m.http_reqs.values.count} peticiones a módulos críticos con LiveKit detenido a mitad de la prueba`,
    `errores ${(m.http_req_failed.values.rate * 100).toFixed(3)} % · p95 ${m.http_req_duration.values['p(95)'].toFixed(1)} ms`,
    `umbrales: ${Object.entries(m).filter(([, v]) => v.thresholds).map(([k, v]) => `${k} ${Object.values(v.thresholds).every((t) => t.ok) ? 'OK' : 'FALLA'}`).join(' · ')}`,
  ].join('\n');
  return { [__ENV.SUMMARY || 'results/eq04-summary.json']: JSON.stringify(data, null, 2), stdout: text + '\n' };
}
