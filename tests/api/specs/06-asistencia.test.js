// RF07 · Asistencia de estudiantes
const { api } = require('../lib/client');
const { teacherSheet } = require('../lib/fixtures');

describe('Registro de asistencia', () => {
  const teacher = () => api('demo-ebr', 'teacher');
  let fx;
  let before;

  beforeAll(async () => {
    fx = await teacherSheet();
    const roster = await teacher().get(`/sections/${fx.sectionId}/attendance`);
    before = roster.body.rows.find((r) => r.student_id === fx.studentId);
  });

  afterAll(async () => {
    // leave the day as it was
    if (before) await teacher().put(`/sections/${fx.sectionId}/attendance`, { marks: [{ person_id: fx.studentId, status: before.status || 'present', note: before.note }] });
  });

  test('la lista del día trae a los estudiantes matriculados de la sección', async () => {
    const res = await teacher().get(`/sections/${fx.sectionId}/attendance`);
    expect(res.status).toBe(200);
    expect(res.body.rows.length).toBe(res.body.section.enrolled);
  });

  test('rechaza un estado que no existe', async () => {
    const res = await teacher().put(`/sections/${fx.sectionId}/attendance`, { marks: [{ person_id: fx.studentId, status: 'dormido' }] });
    expect(res.status).toBe(400);
    expect(res.body.error.fields.marks).toBeDefined();
  });

  test('marca tardanza y el conteo de la sección se actualiza', async () => {
    const res = await teacher().put(`/sections/${fx.sectionId}/attendance`, { marks: [{ person_id: fx.studentId, status: 'late' }] });
    expect(res.status).toBe(200);
    const row = res.body.roster.rows.find((r) => r.student_id === fx.studentId);
    expect(row.status).toBe('late');
    expect(res.body.roster.section.tally.late).toBeGreaterThanOrEqual(1);
  });

  test('el QR de un carnet válido registra la asistencia del estudiante', async () => {
    const codes = await api('demo-ebr', 'director').post('/attendance/card-codes', { kind: 'student', ids: [fx.studentId] });
    expect(codes.status).toBe(200);
    const res = await api('demo-ebr', 'director').post('/attendance/scans', { input: codes.body.codes[0].payload });
    expect(res.status).toBe(200);
    expect(res.body.holder).toEqual(expect.objectContaining({ kind: 'student', id: fx.studentId }));
    expect(['present', 'late']).toContain(res.body.status);
  });

  test('un carnet desconocido no registra asistencia', async () => {
    const res = await api('demo-ebr', 'director').post('/attendance/scans', { input: 'carnet-que-no-existe' });
    expect(res.status).toBe(404);
    expect(res.body.error.code).toBe('card_unknown');
  });
});
