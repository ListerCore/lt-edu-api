# Colección Bruno — ListerCore Edu API

Documentación ejecutable de `lt-edu-api` para [Bruno](https://www.usebruno.com) (v2 o superior, que trae gRPC).

## Uso

1. Bruno → **Open Collection** → esta carpeta (`lt-edu-api/docs/bruno`).
2. Entorno **local** (arriba a la derecha) y rellena `email` y `password` (secreta: no se guarda en git).
   Con `make dev` la API escucha en HTTP `:5050` y gRPC `:5051`.
3. `HTTP › Auth › Sign in`: guarda el token de sesión en `{{token}}`. Desde ahí funcionan HTTP y gRPC.

## Contenido

- `http/` — API REST (`/api/v1`): System, Auth, MFA, Announcements, Attachments. Escrita a mano.
- `grpc/` — una carpeta por servicio de `proto/ltedu`, una petición por rpc, con cuerpo de ejemplo y docs
  (campos de request y response, tipos anidados y enums) sacados de los comentarios de los `.proto`.

Cada petición tiene su pestaña **Docs**; `collection.bru` explica autenticación, errores y límites.

## Regenerar la parte gRPC

Tras cambiar `proto/`:

```sh
python3 scripts/bruno-gen.py   # desde lt-edu-api/, necesita buf
```

Borra y vuelve a escribir `docs/bruno/grpc/`; no edites esos archivos a mano.

Los cuerpos usan los nombres de campo del `.proto` (snake_case). `Timestamp` va como `{"seconds", "nanos"}`.

## Servicios gRPC

| Servicio | Métodos |
|---|---|
| `AcademicService` | 24 |
| `AnnouncementService` | 5 |
| `ApafaService` | 10 |
| `AssessmentService` | 12 |
| `AttendanceService` | 20 |
| `ClassroomService` | 31 |
| `ClosingService` | 15 |
| `ComplianceService` | 13 |
| `CurriculumService` | 18 |
| `DocumentsService` | 27 |
| `ExamsService` | 22 |
| `FinanceService` | 16 |
| `InstitutionService` | 8 |
| `MeetingsService` | 7 |
| `ParticipationService` | 5 |
| `PeopleService` | 27 |
| `ProgramsService` | 18 |
| `ReportsService` | 1 |
| `StaffService` | 6 |
| `SystemService` | 1 |
| `TeachingService` | 11 |
| `TutoringService` | 14 |
| `WellbeingService` | 33 |
