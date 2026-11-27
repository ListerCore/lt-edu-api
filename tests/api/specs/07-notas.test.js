// RF08, RF09 · Notas por competencias y bloqueo de un periodo cerrado
const { api, query } = require('../lib/client');
const { teacherSheet } = require('../lib/fixtures');

describe('Registro de notas', () => {
  const teacher = () => api('demo-ebr', 'teacher');
  let fx;
  let previous;

  const save = (termId, level) =>
    teacher().put(`/assignments/${fx.assignmentId}/levels`, { term_id: termId, marks: [{ student_id: fx.studentId, competency_id: fx.competencyId, level }] });

  beforeAll(async () => {
    fx = await teacherSheet();
    const sheet = await teacher().get(`/assignments/${fx.assignmentId}/sheet${query({ term_id: fx.openTermId })}`);
    previous = sheet.body.sheet.marks.find((m) => m.student_id === fx.studentId && m.competency_id === fx.competencyId);
  });

  afterAll(async () => {
    await save(fx.openTermId, previous?.level ?? '');
  });

  test('el bimestre abierto se puede editar', async () => {
    const res = await teacher().get(`/assignments/${fx.assignmentId}/sheet${query({ term_id: fx.openTermId })}`);
    expect(res.status).toBe(200);
    expect(res.body.sheet).toEqual(expect.objectContaining({ locked: false, can_edit: true, kind: 'literal' }));
  });

  test('solo acepta los niveles AD, A, B o C', async () => {
    const res = await save(fx.openTermId, 'Z');
    expect(res.status).toBe(400);
    expect(res.body.error.fields.marks).toMatch(/AD, A, B o C/);
  });

  test('guarda el nivel de logro en el bimestre abierto', async () => {
    const res = await save(fx.openTermId, 'AD');
    expect(res.status).toBe(200);
    const mark = res.body.sheet.marks.find((m) => m.student_id === fx.studentId && m.competency_id === fx.competencyId);
    expect(mark.level).toBe('AD');
  });

  test('un bimestre cerrado bloquea sus notas (409 term_closed)', async () => {
    const sheet = await teacher().get(`/assignments/${fx.assignmentId}/sheet${query({ term_id: fx.closedTermId })}`);
    expect(sheet.body.sheet.locked).toBe(true);
    const res = await save(fx.closedTermId, 'A');
    expect(res.status).toBe(409);
    expect(res.body.error.code).toBe('term_closed');
  });

  test('el docente no puede editar el curso de otro docente', async () => {
    const director = await api('demo-ebr', 'director').get('/gradebook');
    const other = director.body.sheets.find((s) => s.staff_id && s.assignment_id !== fx.assignmentId && s.section_id !== fx.sectionId);
    const res = await teacher().get(`/assignments/${other.assignment_id}/sheet${query({ term_id: fx.openTermId })}`);
    expect([403, 404]).toContain(res.status);
  });
});
