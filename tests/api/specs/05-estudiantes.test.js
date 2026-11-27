// RF06 · Personas y matrícula: registro de estudiantes
const { api, query, freshDni } = require('../lib/client');

const person = (dni, extra = {}) => ({
  document_type: 'dni', document_number: dni, given_names: 'Prueba', paternal_surname: 'Jest', maternal_surname: 'Api',
  birth_date: '2012-04-10', sex: 'f', ...extra,
});

describe('Registro de estudiantes', () => {
  const secretary = () => api('demo-ebr', 'secretary');
  const dni = freshDni();
  let id;

  afterAll(async () => {
    if (id) await secretary().del(`/students/${id}`);
  });

  test('valida DNI de 8 dígitos y nombres obligatorios', async () => {
    const res = await secretary().post('/students', { document_type: 'dni', document_number: '12', given_names: '' });
    expect(res.status).toBe(400);
    expect(res.body.error.fields).toEqual(expect.objectContaining({ documentNumber: expect.any(String), givenNames: expect.any(String) }));
  });

  test('registra un estudiante y le asigna código', async () => {
    const res = await secretary().post('/students', person(dni));
    expect(res.status).toBe(200);
    id = res.body.student.id;
    expect(res.body.student.code).toMatch(/^\d{4}-\d{4}$/);
    expect(res.body.student.status).toBe('active');
  });

  test('no permite dos estudiantes con el mismo documento', async () => {
    const res = await secretary().post('/students', person(dni));
    expect(res.status).toBe(400);
    expect(res.body.error.fields.documentNumber).toMatch(/documento/);
  });

  test('actualiza los datos del estudiante', async () => {
    const res = await secretary().put(`/students/${id}`, person(dni, { given_names: 'Prueba Editada' }));
    expect(res.status).toBe(200);
    expect(res.body.student.given_names).toBe('Prueba Editada');
  });

  test('lo encuentra por documento y pagina la lista', async () => {
    const res = await secretary().get(`/students${query({ query: dni, limit: 5 })}`);
    expect(res.status).toBe(200);
    expect(res.body.students.map((s) => s.id)).toContain(id);
    expect(res.body.students.length).toBeLessThanOrEqual(5);
  });

  test('elimina un estudiante sin matrícula', async () => {
    const res = await secretary().del(`/students/${id}`);
    expect(res.status).toBe(200);
    expect((await secretary().get(`/students/${id}`)).status).toBe(404);
    id = undefined;
  });
});
