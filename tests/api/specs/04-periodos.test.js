// RF05 · Estructura académica y periodos
const { api } = require('../lib/client');

describe('Periodos académicos', () => {
  const director = () => api('demo-ebr', 'director');
  const created = [];

  afterAll(async () => {
    for (const id of created) await director().del(`/periods/${id}`);
  });

  test('valida nombre y fechas', async () => {
    const res = await director().post('/periods', { name: '', starts_on: '2041-12-01', ends_on: '2041-01-01' });
    expect(res.status).toBe(400);
    expect(res.body.error.code).toBe('validation');
    expect(res.body.error.fields).toEqual(expect.objectContaining({ name: expect.any(String), endsOn: expect.any(String) }));
  });

  test('crea un periodo en planificación con sus bimestres', async () => {
    const res = await director().post('/periods', { name: 'Año lectivo 2041', starts_on: '2041-03-01', ends_on: '2041-12-15' });
    expect(res.status).toBe(200);
    created.push(res.body.period.id);
    expect(res.body.period).toEqual(expect.objectContaining({ name: 'Año lectivo 2041', status: 'planning', term_scheme: 'bimester' }));
    expect(res.body.period.terms).toHaveLength(4);
  });

  test('lee el periodo creado y lo lista', async () => {
    const id = created[0];
    const one = await director().get(`/periods/${id}`);
    expect(one.status).toBe(200);
    expect(one.body.period.id).toBe(id);
    const all = await director().get('/periods');
    expect(all.body.periods.map((p) => p.id)).toContain(id);
  });

  test('elimina un periodo sin secciones', async () => {
    const id = created.pop();
    const res = await director().del(`/periods/${id}`);
    expect(res.status).toBe(200);
    expect((await director().get(`/periods/${id}`)).status).toBe(404);
  });
});
