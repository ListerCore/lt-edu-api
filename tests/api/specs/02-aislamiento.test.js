// EQ-01 · AC01 Seguridad: ninguna institución ve ni cambia datos de otra
const { api, call, query } = require('../lib/client');
const { firstStudent } = require('../lib/fixtures');

describe('EQ-01 Aislamiento entre instituciones', () => {
  let studentOfEba;

  beforeAll(async () => {
    studentOfEba = await firstStudent('demo-eba', 'tutor');
  });

  test('un estudiante de otra institución responde 404 aunque se conozca su id', async () => {
    const res = await api('demo-ebr', 'director').get(`/students/${studentOfEba.id}`);
    expect(res.status).toBe(404);
    expect(JSON.stringify(res.body)).not.toContain(studentOfEba.document_number);
  });

  test('no se puede modificar un estudiante de otra institución', async () => {
    const res = await api('demo-ebr', 'secretary').put(`/students/${studentOfEba.id}`, {
      document_type: 'dni', document_number: studentOfEba.document_number, given_names: 'Intruso',
      paternal_surname: 'Intrusa', maternal_surname: 'Prueba', birth_date: '2000-01-01', sex: 'm',
    });
    expect(res.status).toBe(404);
    const after = await api('demo-eba', 'tutor').get(`/students${query({ query: studentOfEba.document_number })}`);
    expect(after.body.students.find((s) => s.id === studentOfEba.id).given_names).toBe(studentOfEba.given_names);
  });

  test('una sesión de EBR que nombra demo-eba sigue trabajando sobre EBR', async () => {
    const ebrDirector = api('demo-ebr', 'director').token;
    const context = await call('GET', '/api/v1/institutions/demo-eba/context', { token: ebrDirector });
    expect(context.status).toBe(200);
    expect(context.body.context.institution_slug).toBe('demo-ebr');
    const list = await call('GET', '/api/v1/institutions/demo-eba/students?limit=50', { token: ebrDirector });
    expect(list.status).toBe(200);
    expect(list.body.students.map((s) => s.id)).not.toContain(studentOfEba.id);
  });

  test('el carnet de otra institución no registra asistencia aquí', async () => {
    const institute = api('demo-institute', 'director');
    const student = (await institute.get(`/students${query({ limit: 1 })}`)).body.students[0];
    const codes = await institute.post('/attendance/card-codes', { kind: 'student', ids: [student.id] });
    const res = await api('demo-ebr', 'director').post('/attendance/scans', { input: codes.body.codes[0].payload });
    expect(res.status).toBe(404);
    expect(res.body.error.code).toBe('card_unknown');
  });

  test('500 peticiones concurrentes de dos instituciones nunca se cruzan', async () => {
    const callers = [
      ['demo-ebr', 'director'], ['demo-ebr', 'teacher'], ['demo-ebr', 'secretary'], ['demo-eba', 'tutor'],
    ].map(([slug, role]) => ({ slug, client: api(slug, role) }));
    let mismatches = 0;
    for (let round = 0; round < 10; round++) {
      const results = await Promise.all(Array.from({ length: 50 }, async (_, i) => {
        const { slug, client } = callers[(i + round) % callers.length];
        const res = await client.get('/context');
        return res.status === 200 && res.body.context.institution_slug === slug;
      }));
      mismatches += results.filter((ok) => !ok).length;
    }
    expect(mismatches).toBe(0);
  });
});
