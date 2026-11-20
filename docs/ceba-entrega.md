# CEBA: puesta en producción y entrega (fase E9)

Guía para llevar ListerCore Edu a producción en un CEBA y entregarlo. Corresponde a la fase E9 de
`docs/ceba/PLAN.md`. Cada punto dice qué hay listo en la plataforma y qué falta hacer, y quién lo hace.

## 1. Verificación antes de entregar

### Recorrido por rol

`lt-edu-app/scripts/walkthrough.mjs` entra con cada rol y abre todas las páginas que le ofrece su menú. Lo hace en
escritorio y en celular, con tema claro y oscuro. Registra errores de consola, excepciones, pantallas de error y
scroll horizontal en el celular, y deja una captura por página y un informe.

```sh
cd lt-edu-api && go run ./cmd/demosessions -slug demo-eba > /tmp/sessions.json   # una sesión por rol
cd lt-edu-app && node scripts/walkthrough.mjs --sessions /tmp/sessions.json --type EBA --out /tmp/recorrido
cd lt-edu-api && go run ./cmd/demosessions -cleanup                              # borra las sesiones
```

Resultado en `demo-eba`: los 9 roles, en escritorio y en celular, con tema claro y oscuro, recorren su menú
completo (de 8 a 36 páginas) sin errores. Antes de llegar a eso, el recorrido encontró cuatro problemas, ya
corregidos:

- a los estudiantes se les ofrecían Cursos y Notas, que la API les niega;
- Certificados aparecía en EBR y EBA, aunque es la página de los institutos;
- el tablero de tutoría repetía una etiqueta de alerta;
- la ficha de bienestar se caía si la API era anterior a la app.

Chrome en modo headless se traba después de cientos de navegaciones, así que el script abre un navegador nuevo por
rol.

`demosessions` no corre en producción ni fuera de las instituciones `demo-…`. Cada rol entra como una persona real
del demo, para que vea datos reales: dirección, coordinación, secretaría, psicología, tesorería, tutor, docente,
responsable técnico y estudiante.

### Seguridad

Se hizo una revisión independiente de las fases E0 a E8 (aislamiento por institución, permisos por rol, archivos,
mensajes, sondeo de disponibilidad y seguimiento público). Lo corregido:

- **Archivos del aula:**
  - el docente sube material solo a las aulas que enseña;
  - el docente ve solo las entregas de esas aulas;
  - el estudiante ve solo el material de su grado con la unidad abierta;
  - el estudiante no cambia su entrega después de entregarla.
- **Mensajes:** solo el tutor vigente lee y escribe la conversación. Un tutor anterior la pierde.
- **Sondeo de disponibilidad:**
  - no se conecta a direcciones privadas, de loopback ni link-local, tampoco en redirecciones;
  - la comprobación a pedido tiene un intervalo mínimo de 30 s.
- **Cumplimiento:** «verificado» y «no aplica» los marca solo el responsable técnico. Si se quita la evidencia de una
  fila verificada, vuelve a contar como pendiente.
- **Cuestionarios:** las respuestas no se muestran mientras al estudiante le queden intentos.
- **Seguimiento público por QR:** ya no publica los códigos de verificación ni las notas internas.

Queda una decisión de política: dirección y coordinación pueden **leer** (no escribir) las conversaciones
tutor–estudiante.

### Carga

`cmd/loadtest` simula estudiantes que listan sus aulas, abren material y evaluaciones y, con `-write`, inician
intentos y los entregan. Las escrituras cambian datos, así que se corre contra una copia: un respaldo restaurado en
otra base, con una segunda API apuntando a ella.

Resultado en el equipo de desarrollo (una sola API, PostgreSQL en Docker), con 200 estudiantes simultáneos durante
60 s:

| Ritmo                            | Peticiones/s | Errores | p50     | p99    |
| -------------------------------- | -----------: | ------: | ------- | ------ |
| Pausa media de 2 s entre acciones |          107 |       0 | 12–36 ms | ≤ 131 ms |
| Pausa media de 0,5 s (pico duro)  |          376 |       0 | 23–70 ms | ≤ 472 ms |

El CEBA demo tiene unas 230 matrículas, así que el pico duro equivale a todo el CEBA trabajando a la vez. Hay que
repetir la prueba en la instancia de producción antes de abrirla.

## 2. Producción

Los datos de alojamiento son la decisión **D8**: proveedor, instancia, dominio y vigencia del servicio. También se
registran en *Configuración › Institución › Ficha de la plataforma*, porque el informe 6.2.1 los toma de ahí.

| Tarea                                      | Cómo                                                                                                                     |
| ------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------ |
| Instancia, dominio y certificado TLS       | La API queda interna; solo lt-edu-app la alcanza por la red. El dominio apunta a lt-edu-app con HTTPS (Let's Encrypt del panel de hosting). |
| Variables                                  | `APP__ENV=production`, base de datos, Redis, almacenamiento (MinIO/S3), correo, `APP__SITE__URL` con el dominio real y `APP__UPTIME__INTERVAL=15m`. |
| Migraciones y datos iniciales              | El `entrypoint.sh` corre `migrate up` y `seed` al arrancar.                                                              |
| Respaldo diario                            | `scripts/backup.sh` desde cron, con copia fuera del servidor (`BACKUP_COPY_CMD`). El bucket de archivos, con versionado o réplica. |
| Restauración probada                       | `scripts/restore-check.sh <dump>` cada semana: restaura en una base aparte y compara los conteos con la base viva.        |
| Monitoreo                                  | El monitor de la API registra la URL cada 15 min (*Cumplimiento DRE › Disponibilidad*). Hay que sumar un monitor externo sobre `/api/v1/health` que avise por correo o WhatsApp. |
| Registros                                  | Los registros de la API y la app en JSON, con retención de al menos 30 días en el panel de hosting.                      |

Comprobado en desarrollo: el respaldo de 14 MB se restauró completo, con todas las tablas iguales a la base viva y la
migración 34.

## 3. Datos reales

1. En el panel, dirección configura la institución: autorizaciones, ficha de la plataforma, ciclos y grados,
   periodo promocional, secciones con su forma de atención, docentes y tutores.
2. Secretaría carga la nómina en *Matrícula › Importar nómina* con un CSV. La plantilla trae las mismas columnas que
   la nómina exportada para el SIAGIE.
   - Cada fila se revisa con las reglas de la matrícula manual: edad mínima, forma autorizada, vacantes y documentos
     repetidos.
   - Un estudiante que ya existe no se duplica.
   - Se importa solo cuando no quedan errores.
3. **Conciliación:** la pantalla muestra, por sección, cuántos había, cuántos llegan del archivo y el total. Esos
   totales se comparan con la lista del CEBA y con la nómina del SIAGIE. Para cerrar la carga, la nómina en PDF
   (*Matrícula › Nómina y SIAGIE*) se firma con la dirección.

## 4. Capacitación

Una sesión por rol, de 45 a 60 min, sobre el CEBA ya cargado:

| Rol                 | Contenido                                                                                                          |
| ------------------- | ------------------------------------------------------------------------------------------------------------------ |
| Dirección           | Configuración, firmantes, firma de actas y constancias, reglas de promoción, tablero de tutoría, cumplimiento DRE. |
| Secretaría          | Importar nómina, matrícula y ficha única, ubicación y actas, SIAGIE (nómina y notas), constancias.                 |
| Coordinación        | Aulas virtuales, evaluaciones, cierre del periodo y actas, permanencia, asignación de tutores.                      |
| Docentes            | Unidades y material, evaluaciones en línea y calificación, foros, registro de notas.                               |
| Tutores virtuales   | Tablero, alertas de abandono, mensajes, tutorías, compromisos, derivaciones.                                       |
| Responsable técnico | Equipos, pruebas, evidencias, verificación y los dos informes, disponibilidad, respaldos.                          |
| Estudiantes         | Ingreso desde el celular, aula, entregas, foros, «Mi tutoría».                                                     |

## 5. Expediente para la DRE

1. El responsable técnico completa *Cumplimiento DRE*: ambiente, equipos y pruebas con sus capturas, y una evidencia
   por fila. Las capturas se toman con perfiles de prueba, sin contraseñas ni datos personales.
2. Marca cada fila como verificada. El PDF deja de salir como «BORRADOR».
3. Completa los datos del informe: número, fecha de evaluación, resultado, limitaciones y conclusión. Descarga los
   dos PDF.
4. Firma los informes y los suma al expediente con los anexos numerados y foliados (anexo y folio de cada fila).

## 6. Acta de entrega

> **ACTA DE ENTREGA DE LA PLATAFORMA EDUCATIVA VIRTUAL**
>
> En ____________, a los ___ días del mes de __________ de 20__, se reúnen, por el CEBA ________________________,
> ________________________ (director/a), y por ListerCore, ________________________, para dejar constancia de la
> entrega de la plataforma ListerCore Edu, disponible en ________________________.
>
> 1. **Módulos entregados:** matrícula EBA, ubicación y SIAGIE; aula virtual con evaluaciones y foros; participación
>    y permanencia; tutoría virtual; evaluación, cierre, actas y constancias; cumplimiento DRE.
> 2. **Datos cargados:** ___ estudiantes en ___ secciones del periodo promocional ______, conciliados con la nómina
>    del CEBA.
> 3. **Capacitación:** realizada a ___ personas de los roles _______________ (lista de asistencia adjunta).
> 4. **Respaldo:** diario, con restauración probada el __/__/____.
> 5. **Acompañamiento:** ___ semanas desde la fecha, por ________________ (canal), con respuesta en ___ horas
>    hábiles.
> 6. **Observaciones:** ____________________________________________.
>
> Firmas: ______________________ (CEBA) ______________________ (ListerCore)
