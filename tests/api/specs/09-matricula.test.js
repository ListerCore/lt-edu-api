// RF06 · Matrícula, retiro y la regla "nada académico se borra"
const { api, query, freshDni } = require('../lib/client');

describe('Matrícula y retiro', () => {
  const secretary = () => api('demo-ebr', 'secretary');
  let studentId;
  let enrollmentId;
  let target;

  beforeAll(async () => {
    const periods = await secretary().get('/periods');
    const periodId = periods.body.periods.find((p) => p.status === 'active').id;
    const board = await secretary().get(`/enrollments${query({ period_id: periodId, limit: 1 })}`);
    const used = Object.fromEntries(board.body.occupancy.map((o) => [o.section_id, o.active]));
    const section = board.body.sections.find((s) => (used[s.id] ?? 0) < s.capacity);
    target = { period_id: periodId, level_id: section.level_id, grade_id: section.grade_id, section_id: section.id };
    const created = await secretary().post('/students', {
      document_type: 'dni', document_number: freshDni(), given_names: 'Prueba', paternal_surname: 'Matricula', maternal_surname: 'Jest', birth_date: '2015-04-10', sex: 'm',
    });
    studentId = created.body.student.id;
  });

  afterAll(async () => {
    if (enrollmentId) await secretary().del(`/enrollments/${enrollmentId}`);
    if (studentId) await secretary().del(`/students/${studentId}`);
  });

  test('matricula al estudiante en una sección con vacantes', async () => {
    const res = await secretary().post('/enrollments', { student_id: studentId, kind: 'new', ...target });
    expect(res.status).toBe(200);
    enrollmentId = res.body.enrollment.id;
    expect(res.body.enrollment).toEqual(expect.objectContaining({ student_id: studentId, section_id: target.section_id, status: 'active' }));
  });

  test('no permite matricularlo dos veces en el mismo periodo', async () => {
    const res = await secretary().post('/enrollments', { student_id: studentId, kind: 'new', ...target });
    expect(res.status).toBe(409);
    expect(res.body.error.code).toBe('already_enrolled');
  });

  test('un estudiante con matrícula no se elimina: se cambia su estado', async () => {
    const res = await secretary().del(`/students/${studentId}`);
    expect(res.status).toBe(409);
    expect(res.body.error.code).toBe('student_enrolled');
  });

  test('registra el retiro como cambio de estado', async () => {
    const res = await secretary().post(`/enrollments/${enrollmentId}/end`, { status: 'withdrawn', ended_on: new Date().toISOString().slice(0, 10) });
    expect(res.status).toBe(200);
    expect(res.body.enrollment.status).toBe('withdrawn');
  });

  test('el retiro se puede revertir', async () => {
    const res = await secretary().post(`/enrollments/${enrollmentId}/end`, { status: 'active' });
    expect(res.status).toBe(200);
    expect(res.body.enrollment.status).toBe('active');
  });

  test('el docente no puede matricular', async () => {
    const res = await api('demo-ebr', 'teacher').post('/enrollments', { student_id: studentId, kind: 'new', ...target });
    expect(res.status).toBe(403);
  });
});
