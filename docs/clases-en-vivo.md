# Clases en vivo

Sesiones en vivo de tres orígenes, cada una con su anfitrión, que elige **dónde** ocurre la videollamada:

| Origen (`source`) | Se programa en                         | Anfitrión                           | Participantes                    |
| ----------------- | -------------------------------------- | ----------------------------------- | -------------------------------- |
| `class`           | Aula virtual → material «Sesión en vivo» | Docente del aula, dirección, coordinación | Estudiantes del grado (ítem publicado, unidad abierta) |
| `tutoring`        | Tutoría virtual → agendar (modalidad virtual) | Tutor de la sesión, dirección, coordinación | Estudiantes convocados |
| `psychology`      | Psicología → caso → Citas               | Psicología                          | El estudiante del caso           |

Proveedores (`provider`): `platform` (video dentro de ListerCore Edu con LiveKit; la API firma un token por persona y
sala `<source>-<id>`) o `meet` (enlace externo de Google Meet u otro servicio). Los participantes entran desde 15 minutos
antes hasta 30 minutos después del final; el anfitrión, cuando quiera. Cada ingreso queda en `live.joins` (asistencia de
la llamada); el de un estudiante a una clase cuenta además como vista del ítem (participación, E5).

## Configuración

| Variable                    | Ejemplo                    | Nota                                             |
| --------------------------- | -------------------------- | ------------------------------------------------ |
| `APP__LIVEKIT__URL`         | `wss://live.listercore.es` | Vacía: solo se ofrece Google Meet                |
| `APP__LIVEKIT__API_KEY`     | `APIxxxx`                  |                                                  |
| `APP__LIVEKIT__API_SECRET`  | (secreto)                  | Firma HS256 de los tokens                         |

Local: `make livekit/start` (LiveKit en modo dev, `ws://localhost:7880`, `devkey` / `secret`, ya en `.env.example`).
Producción: un servidor LiveKit propio (puertos 7880 TCP con TLS, 7881 TCP y 7882 UDP abiertos) o LiveKit Cloud.
