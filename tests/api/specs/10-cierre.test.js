// RF05, RF09 · Estados del periodo y cierre de sección
const { api } = require('../lib/client');

describe('Estados del periodo', () => {
  const director = () => api('demo-ebr', 'director');
  let periodId;

  beforeAll(async () => {
    const res = await director().post('/periods', { name: 'Año lectivo 2043', starts_on: '2043-03-01', ends_on: '2043-12-15' });
    periodId = res.body.period.id;
  });

  afterAll(async () => {
    if (periodId) await director().del(`/periods/${periodId}`);
  });

  test('no se activa un segundo periodo mientras otro está en curso', async () => {
    const res = await director().patch(`/periods/${periodId}/status`, { status: 'active' });
    expect(res.status).toBe(409);
    expect(res.body.error.code).toBe('active_period_taken');
  });

  test('rechaza un estado que no existe', async () => {
    const res = await director().patch(`/periods/${periodId}/status`, { status: 'inventado' });
    expect(res.status).toBe(400);
    expect(res.body.error.code).toBe('invalid_status');
  });
});

describe('Cierre de sección', () => {
  const director = () => api('demo-ebr', 'director');
  let periodId;
  let sectionId;

  beforeAll(async () => {
    const book = await api('demo-ebr', 'teacher').get('/gradebook');
    periodId = book.body.period.id;
    sectionId = book.body.sheets[0].section_id;
  });

  test('el resumen del cierre lista las secciones del periodo', async () => {
    const res = await director().get(`/periods/${periodId}/closing`);
    expect(res.status).toBe(200);
    expect(res.body.sections.length).toBeGreaterThan(0);
    expect(res.body.last_term_closed).toBe(false);
  });

  test('no se confirma la situación final con el último bimestre abierto', async () => {
    const res = await director().post(`/sections/${sectionId}/closing/confirm`, {});
    expect(res.status).toBe(409);
    expect(res.body.error.code).toBe('last_term_open');
  });
});
