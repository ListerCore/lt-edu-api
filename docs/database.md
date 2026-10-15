# Modelo de datos SIGE-JL

Plataforma **SaaS**: muchas instituciones (CEBA, colegios EBR) en una sola base PostgreSQL 18 (`uuidv7()`, UTF-8, America/Lima). Migraciones en `migrations/` (`make migrate/up`); configuración de clientes en `seeds/` (`make db/seed`).

Fuentes: propuesta SIGE-JL, informe técnico (RSG N.° 613-2014-MINEDU, Anexo 2), pantallas de `joseph-app` y la normativa curricular del MINEDU (abajo).

## Esquemas

| Migración | Esquema | Alcance | Qué guarda |
|---|---|---|---|
| 0002 | `util`, `audit` | — | triggers comunes, bitácora de cambios y de actividad |
| 0003 | `curriculum` | global | modalidades, ciclos, grados, áreas, 31 competencias CNEB, 110 capacidades, plan oficial por ciclo |
| 0004 | `tenant` | plataforma | institución, servicios educativos (código modular), sedes |
| 0004 | `core` | institución | persona, estudiante, personal, apoderado |
| 0004 | `iam` | global + institución | cuentas (globales), membresías y roles por institución, sesiones, 2FA |
| 0005 | `academic` | institución | plan de estudios propio, periodo, bimestres, sección, aula |
| 0006 | `enrollment` | institución | postulación, ubicación/convalidación, matrícula, expediente |
| 0007 | `lms`, `attendance` | institución | sesiones, recursos, tareas, entregas, foros, asistencia |
| 0008 | `assessment` | institución | evaluaciones, criterios, resultados, libreta, acta final |
| 0009 | `wellbeing`, `comms` | institución | alertas, intervenciones, protección, comunicados, mensajería |
| 0010 | `finance` | institución | cronograma, convenios, cargos, pagos, comprobantes SUNAT |
| 0011 | `ai`, `site` | institución | IA con revisión humana, riesgo, reclamaciones, ARCO, contacto |
| 0011 | `ops` | plataforma | respaldos |
| 0012 | — | — | rol `joseph_app` y políticas RLS |

## Multi-institución (SaaS)

- **Cuenta global, datos por institución.** `iam.account` es el login (correo + contraseña + 2FA). `iam.membership` la une a cada institución con una `core.person` propia y sus roles. Un docente que trabaja en dos colegios tiene una cuenta y dos membresías.
- **Las personas son de cada institución** (Ley N.° 29733: cada institución responde por sus datos). El mismo DNI en dos instituciones son dos filas independientes.
- **Servicios educativos.** `tenant.service` registra lo autorizado (modalidad + nivel, código modular, forma de atención). No se puede crear una sección de un nivel no autorizado.
- **Aislamiento doble:**
  1. **FKs compuestas** `(institution_id, x_id)`: ninguna fila puede apuntar a datos de otra institución, ni siquiera como superusuario.
  2. **RLS**: el rol `joseph_app` solo ve filas de `app.institution_id`.

### Lo que la API debe hacer

```sql
-- al abrir cada conexión del pool (el usuario joseph es superusuario y salta RLS)
SET ROLE joseph_app;

-- en cada transacción
SET LOCAL app.institution_id = '<institución activa>';
SET LOCAL app.account_id     = '<cuenta autenticada>';
```

Sin `app.institution_id`, `joseph_app` no ve ninguna fila de institución. `iam.account`, `iam.session` e `iam.login_event` no tienen RLS: son de la cuenta y los filtra la API. Las tablas nuevas deben terminar su migración con `SELECT util.apply_tenant_security();`.

## Currículo (normativa MINEDU)

| Modalidad | Ciclos → grados | Fuente |
|---|---|---|
| EBR Inicial | I (0–2 años), II (3–5 años) | Programa Curricular de Educación Inicial |
| EBR Primaria | III (1.°–2.°), IV (3.°–4.°), V (5.°–6.°) | CNEB, Tabla N.° 4 y Cuadro N.° 1 |
| EBR Secundaria | VI (1.°–2.°), VII (3.°–5.°) | CNEB, Tabla N.° 4 y Cuadro N.° 1 |
| EBA | Inicial (1.°–2.°), Intermedio (1.°–3.°), Avanzado (1.°–4.°) | RVM N.° 034-2019-MINEDU (reemplaza la Tabla N.° 10 del CNEB) |

- `curriculum.cycle_competency` dice en qué área se trabaja cada competencia en cada ciclo, o si es **transversal** (`area_code` null). Los conteos coinciden con el CNEB: ciclo I = 7, ciclo II = 14, primaria = 30, secundaria = 31.
- En **EBA inicial e intermedio** son transversales TIC (28), autonomía (29), Arte (5, 6) y Educación Física (2, 3, 4). En **avanzado** Arte y Educación Física son áreas propias. **Inglés** en intermedio solo trabaja la competencia oral (13).
- La regla de dónde se puede evaluar cada competencia la aplica `curriculum.is_competency_of()`. Un aula solo evalúa competencias de su área, transversales de su ciclo, o las del taller.
- **Áreas complementarias** (`kind`): `tutoring` (Tutoría y Orientación Educativa) y los talleres `workshop`: *TIC y Competencia Digital* (comp. 28) y *Certificación Técnica Modular* (comp. 27). Cada institución arma su plan en `academic.study_plan_area`. Admite cualquier área oficial del ciclo más tutoría y talleres; rechaza áreas que no son del ciclo.
- Marcas normativas: Educación Religiosa es exonerable (Ley N.° 29635), con `final_grade.exempted`. Castellano como segunda lengua es solo para II.EE. bilingües (`bilingual_only`).

## Otras decisiones

- Estados como `text + CHECK` (no `ENUM`); códigos en inglés, traducción en el frontend.
- Escala CNEB como dominio `curriculum.achievement_level` (AD, A, B, C).
- La matrícula solo admite una sección del mismo periodo y grado (FK compuesta). Hay un periodo activo por institución y modalidad.
- El estado de las cuotas se calcula en `finance.charge_balance`. Un constraint trigger impide asignar más dinero del pagado o cubrir más de lo adeudado.
- `audit.change_log` registra notas, matrícula, pagos, permisos, intervenciones, protección y mensajes, con institución y actor. Lo escribe un trigger `SECURITY DEFINER`; `joseph_app` solo lo lee.
- Mensajes y posts de foro no se borran físicamente: se marcan.
- Los archivos van al almacenamiento de objetos; la base guarda `storage_key` + `sha256`.

## Pendiente

1. **Datos reales del CEBA** en `seeds/ceba_joseph_lister.sql`: RUC, códigos modulares, resolución, bimestres.
2. **Horas por área**: el RVM 034-2019 solo fija EPT (2 h, inicial/intermedio) e Inglés (1 h, intermedio); el resto lo define cada institución.
3. **Desempeños y estándares** por grado (descripciones de nivel) no están cargados; solo competencias y capacidades.
4. **Correlativos** (`EBA-2026-0842`, `LR-2026-000123`, series de boletas) por institución y año: los genera la API.
5. **Alta de instituciones** (onboarding SaaS) y facturación de la suscripción: fuera de este modelo por ahora.
6. **API**: aplicar `SET ROLE joseph_app` en el pool de `internal/adapter/outbound/postgres/client.go`.
