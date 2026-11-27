// EQ-03 · AC03 Rendimiento: estudiantes, docentes y dirección consultan y registran al mismo tiempo, con una rampa
// progresiva hasta 2 000 usuarios virtuales concurrentes. Meta: p95 ≤ 500 ms y menos de 1 % de errores.
//
//   SESSIONS=sessions.json API_URL=http://host.docker.internal:5050 k6 run eq03-rendimiento.js
//
// sessions.json is {"demo-ebr": {...roles}, "demo-eba": {...}} as written by tests/load/run.sh (cmd/demosessions)
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend } from 'k6/metrics';

const API = (__ENV.API_URL || 'http://localhost:5050').replace(/\/$/, '');
const SESSIONS = JSON.parse(open(__ENV.SESSIONS || './sessions.json'));
const PEAK = Number(__ENV.PEAK_VUS || 2000);
const SCALE = Number(__ENV.SCALE || 1); // 0.1 = a 10x shorter smoke run

const stage = (seconds, target) => ({ duration: `${Math.max(5, Math.round(seconds * SCALE))}s`, target });

export const options = {
  scenarios: {
    hora_punta: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        stage(60, Math.round(PEAK * 0.1)),
        stage(60, Math.round(PEAK * 0.25)),
        stage(60, Math.round(PEAK * 0.5)),
        stage(120, PEAK),
        stage(120, PEAK),
        stage(30, 0),
      ],
      gracefulRampDown: '30s',
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<500'],
    http_req_failed: ['rate<0.01'],
    checks: ['rate>0.99'],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

const byCall = {
  contexto: new Trend('t_contexto', true),
  asistencia_resumen: new Trend('t_asistencia_resumen', true),
  lista_seccion: new Trend('t_lista_seccion', true),
  marcar_asistencia: new Trend('t_marcar_asistencia', true),
  registro_notas: new Trend('t_registro_notas', true),
  estudiantes: new Trend('t_estudiantes', true),
};

const auth = (token) => ({ headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' } });
const base = (slug) => `${API}/api/v1/institutions/${slug}`;

// setup finds the teacher's section and a student once, so every VU works on real data
export function setup() {
  const teacher = SESSIONS['demo-ebr'].teacher;
  const book = http.get(`${base('demo-ebr')}/gradebook`, auth(teacher)).json();
  const sectionId = book.sheets[0].section_id;
  const roster = http.get(`${base('demo-ebr')}/sections/${sectionId}/attendance`, auth(teacher)).json();
  return { sectionId, studentIds: roster.rows.map((r) => r.student_id) };
}

function timed(name, res, status = 200) {
  byCall[name].add(res.timings.duration);
  check(res, { [`${name} ${status}`]: (r) => r.status === status });
}

export default function (data) {
  const ebr = SESSIONS['demo-ebr'];
  const eba = SESSIONS['demo-eba'];
  const roll = Math.random();

  if (roll < 0.35) {
    // students and families of the virtual CEBA open the panel
    const token = eba.tutor || eba.student;
    timed('contexto', http.get(`${base('demo-eba')}/context`, auth(token)));
  } else if (roll < 0.55) {
    // teacher takes attendance of the section
    timed('lista_seccion', http.get(`${base('demo-ebr')}/sections/${data.sectionId}/attendance`, auth(ebr.teacher)));
    if (Math.random() < 0.3) {
      const student = data.studentIds[Math.floor(Math.random() * data.studentIds.length)];
      const status = Math.random() < 0.8 ? 'present' : 'late';
      timed('marcar_asistencia', http.put(`${base('demo-ebr')}/sections/${data.sectionId}/attendance`, JSON.stringify({ marks: [{ person_id: student, status }] }), auth(ebr.teacher)));
    }
  } else if (roll < 0.75) {
    // teacher opens the gradebook
    timed('registro_notas', http.get(`${base('demo-ebr')}/gradebook`, auth(ebr.teacher)));
  } else if (roll < 0.9) {
    // office checks the attendance of the day
    timed('asistencia_resumen', http.get(`${base('demo-ebr')}/attendance/overview`, auth(ebr.director)));
  } else {
    // secretary looks up students
    timed('estudiantes', http.get(`${base('demo-ebr')}/students?limit=20`, auth(ebr.secretary)));
  }
  // think time of a real user between clicks
  sleep(1 + Math.random() * 2);
}

export function handleSummary(data) {
  const out = __ENV.SUMMARY || 'results/eq03-summary.json';
  return { [out]: JSON.stringify(data, null, 2), stdout: textSummary(data) };
}

function textSummary(data) {
  const m = data.metrics;
  const ms = (v) => `${v.toFixed(1)} ms`;
  const lines = [
    `EQ-03 · pico ${PEAK} VUs · ${m.http_reqs.values.count} peticiones · ${m.http_reqs.values.rate.toFixed(1)} req/s`,
    `http_req_duration  p95=${ms(m.http_req_duration.values['p(95)'])}  p99=${ms(m.http_req_duration.values['p(99)'])}  avg=${ms(m.http_req_duration.values.avg)}`,
    `http_req_failed    ${(m.http_req_failed.values.rate * 100).toFixed(2)} %`,
    ...Object.keys(byCall).filter((k) => m[`t_${k}`]).map((k) => `  ${k.padEnd(20)} p95=${ms(m[`t_${k}`].values['p(95)'])}`),
    `umbrales: ${Object.entries(m).filter(([, v]) => v.thresholds).map(([k, v]) => `${k} ${Object.values(v.thresholds).every((t) => t.ok) ? 'OK' : 'FALLA'}`).join(' · ')}`,
  ];
  return lines.join('\n') + '\n';
}
