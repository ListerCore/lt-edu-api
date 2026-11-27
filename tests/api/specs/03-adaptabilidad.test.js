// EQ-02 · AC02 Adaptabilidad (RF01, RF02, RF04): el tipo de institución define nombres, periodos y escala, y el
// rol define qué puede hacer cada usuario, sin código por cliente
const { api } = require('../lib/client');

describe('EQ-02 Adaptación por tipo de institución', () => {
  let ebr;
  let eba;

  beforeAll(async () => {
    ebr = (await api('demo-ebr', 'director').get('/context')).body.context;
    eba = (await api('demo-eba', 'tutor').get('/context')).body.context;
  });

  test('cada institución declara su tipo', () => {
    expect(ebr.institution_kind).toBe('ebr');
    expect(eba.institution_kind).toBe('eba');
  });

  test('el nombre del periodo sale del tipo: "Año lectivo" en EBR, distinto en EBA', () => {
    expect(ebr.vocabulary.period).toBe('Año lectivo');
    expect(eba.vocabulary.period).not.toBe(ebr.vocabulary.period);
    expect(ebr.period_kind).not.toBe(eba.period_kind);
  });

  test('los periodos creados toman el tipo de la institución', async () => {
    const res = await api('demo-ebr', 'director').get('/periods');
    expect(res.status).toBe(200);
    for (const p of res.body.periods) expect(p.kind).toBe(ebr.period_kind);
  });

  test('un instituto usa semestres y escala vigesimal (0 a 20) con el mismo código', async () => {
    const context = (await api('demo-institute', 'director').get('/context')).body.context;
    expect(context.institution_kind).toBe('institute');
    expect(context.period_kind).toBe('semester');
    const book = await api('demo-institute', 'teacher').get('/gradebook');
    expect(book.body.scale).toBe('vigesimal');
  });

  test('la escala de calificación de EBR es literal (AD, A, B, C)', async () => {
    const res = await api('demo-ebr', 'teacher').get('/gradebook');
    expect(res.status).toBe(200);
    expect(res.body.scale).toBe('literal');
  });
});

describe('Permisos por rol (RF04)', () => {
  test('el docente no puede crear periodos', async () => {
    const res = await api('demo-ebr', 'teacher').post('/periods', { name: 'Año lectivo 2040', starts_on: '2040-03-01', ends_on: '2040-12-15' });
    expect(res.status).toBe(403);
    expect(res.body.error.code).toBe('academic_read_only');
  });

  test('el docente no puede registrar estudiantes', async () => {
    const res = await api('demo-ebr', 'teacher').post('/students', { document_type: 'dni', document_number: '91234567', given_names: 'X', paternal_surname: 'Y', maternal_surname: 'Z', birth_date: '2012-01-01', sex: 'f' });
    expect(res.status).toBe(403);
    expect(res.body.error.code).toBe('people_read_only');
  });

  test('tesorería no puede ver las notas', async () => {
    const res = await api('demo-ebr', 'treasurer').get('/gradebook');
    expect(res.status).toBe(403);
    expect(res.body.error.code).toBe('grades_forbidden');
  });

  test('el docente solo ve sus propios cursos en el registro de notas', async () => {
    const res = await api('demo-ebr', 'teacher').get('/gradebook');
    expect(res.body.own_only).toBe(true);
    const staff = new Set(res.body.sheets.map((s) => s.staff_id));
    expect(staff.size).toBe(1);
  });
});
