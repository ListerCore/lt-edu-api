// EQ-02 · AC02 Adaptabilidad (RF01, RF04): dirección apaga un módulo opcional y, desde la petición siguiente, la API
// deja de atenderlo, sin cambiar código ni volver a desplegar
const { api } = require('../lib/client');

describe('EQ-02 Módulos opcionales por institución', () => {
  const director = () => api('demo-ebr', 'director');
  const treasurer = () => api('demo-ebr', 'treasurer');

  afterAll(async () => {
    await director().put('/modules/payments', { enabled: true });
  });

  test('lista los módulos opcionales; solo dirección puede cambiarlos', async () => {
    const asDirector = await director().get('/modules');
    expect(asDirector.status).toBe(200);
    expect(asDirector.body.can_manage).toBe(true);
    expect(asDirector.body.modules.map((m) => m.key)).toEqual(expect.arrayContaining(['payments', 'classroom', 'mock-exams']));
    const asTeacher = await api('demo-ebr', 'teacher').get('/modules');
    expect(asTeacher.body.can_manage).toBe(false);
  });

  test('el docente no puede apagar un módulo', async () => {
    const res = await api('demo-ebr', 'teacher').put('/modules/payments', { enabled: false });
    expect(res.status).toBe(403);
    expect(res.body.error.code).toBe('modules_read_only');
  });

  test('un módulo que no existe responde 400', async () => {
    const res = await director().put('/modules/inventado', { enabled: false });
    expect(res.status).toBe(400);
    expect(res.body.error.code).toBe('module_unknown');
  });

  test('con pagos activo, tesorería consulta sus planes', async () => {
    expect((await treasurer().get('/finance/plans')).status).toBe(200);
  });

  test('al apagar pagos, la API rechaza el módulo en la petición siguiente', async () => {
    const off = await director().put('/modules/payments', { enabled: false });
    expect(off.status).toBe(200);
    expect(off.body.module).toEqual({ key: 'payments', name: expect.any(String), enabled: false });

    const plans = await treasurer().get('/finance/plans');
    expect(plans.status).toBe(403);
    expect(plans.body.error.code).toBe('module_disabled');

    const management = await treasurer().get('/management');
    expect(management.body.disabled_modules).toContain('payments');
  });

  test('apagar pagos no afecta a otra institución ni a los módulos base', async () => {
    expect((await api('demo-ebr', 'teacher').get('/gradebook')).status).toBe(200);
    expect((await api('demo-institute', 'treasurer').get('/finance/plans')).status).toBe(200);
  });

  test('al volver a encenderlo, responde de nuevo', async () => {
    expect((await director().put('/modules/payments', { enabled: true })).status).toBe(200);
    expect((await treasurer().get('/finance/plans')).status).toBe(200);
    expect((await treasurer().get('/management')).body.disabled_modules).not.toContain('payments');
  });

  test('cada cambio queda en la auditoría', async () => {
    const res = await director().get('/audit-events?module=institution');
    expect(res.status).toBe(200);
    const summaries = (res.body.events ?? []).map((e) => e.summary);
    expect(summaries.some((s) => /módulo Pensiones y pagos/.test(s))).toBe(true);
  });
});
