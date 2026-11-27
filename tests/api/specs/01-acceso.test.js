// RF03, RF04 · Acceso: sin sesión no hay datos, y los errores tienen la misma forma en toda la API
const { api, call } = require('../lib/client');

describe('Acceso a la API REST', () => {
  test('sin token responde 401 unauthenticated', async () => {
    const res = await api('demo-ebr').get('/periods');
    expect(res.status).toBe(401);
    expect(res.body.error.code).toBe('unauthenticated');
  });

  test('un token inventado responde 401', async () => {
    const res = await call('GET', '/api/v1/institutions/demo-ebr/periods', { token: 'f'.repeat(64) });
    expect(res.status).toBe(401);
  });

  test('una ruta que no existe responde 404 not_found', async () => {
    const res = await api('demo-ebr', 'director').get('/no-existe');
    expect(res.status).toBe(404);
    expect(res.body).toEqual({ error: { code: 'not_found', message: expect.any(String) } });
  });

  test('un JSON mal formado responde 400 invalid_request', async () => {
    const res = await api('demo-ebr', 'director').post('/periods', '{malo');
    expect(res.status).toBe(400);
    expect(res.body.error.code).toBe('invalid_request');
  });

  test('el documento OpenAPI publica las rutas REST', async () => {
    const res = await call('GET', '/api/docs/openapi.json');
    expect(res.status).toBe(200);
    expect(res.body.swagger).toBe('2.0');
    expect(Object.keys(res.body.paths)).toContain('/api/v1/institutions/{institution_slug}/students');
  });
});
