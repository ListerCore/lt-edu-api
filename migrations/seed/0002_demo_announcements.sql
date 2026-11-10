-- Sample announcements for every demo institution, so the panel and the public site start with content.
-- Slugs are 18 random lowercase hex characters (a subset of [a-z0-9])
WITH items (kind, audience, title, body, is_public, pinned, days_ago) AS (
    VALUES
        ('notice', 'community', 'Ya está disponible el horario del periodo en curso', 'Revisa tu horario en el panel. Los cambios de aula y de docente se publican aquí mismo.', true, true, 7),
        ('alert', 'community', 'Mantenimiento programado el sábado de 22:00 a 23:00', 'Durante ese horario no podrás ingresar a tu cuenta. Planifica tus entregas con anticipación.', true, false, 8),
        ('announcement', 'students', 'Actualiza tu foto para la emisión de carnets', 'Sube una foto reciente, de frente y con fondo claro, desde tu cuenta antes del 15 de noviembre.', true, false, 9),
        ('alert', 'guardians', 'Las inasistencias se justifican dentro de las 48 horas', 'Presenta el sustento en secretaría o súbelo desde el panel; pasado el plazo la falta queda registrada.', true, false, 11),
        ('notice', 'staff', 'Reunión general de coordinación', 'Viernes a las 4:00 p. m. en la sala de profesores. Se revisará el avance del periodo y el calendario de evaluaciones.', false, false, 12),
        ('announcement', 'community', 'Abiertas las inscripciones de admisión', 'Consulta requisitos, fechas y vacantes en la sección de Admisión o escríbenos desde Contacto.', true, true, 14),
        ('notice', 'students', 'Publicación de notas al cierre de la evaluación', 'Las notas se publican en el panel dentro de los cinco días hábiles posteriores a cada cierre.', true, false, 17),
        ('alert', 'guardians', 'Vence el pago de la pensión del mes', 'El último día de pago sin mora es el 30. Puedes revisar tu estado de cuenta desde el panel.', false, false, 19),
        ('announcement', 'students', 'Semana de orientación vocacional', 'Charlas y talleres con el área de tutoría y psicología. Inscríbete con tu tutor.', true, false, 21)
)
INSERT INTO comms.announcements (institution_id, slug, kind, audience, title, body, is_public, pinned, published_at)
SELECT i.id, substr(md5(random()::text || clock_timestamp()::text || i.id::text || it.title), 1, 18),
       it.kind, it.audience, it.title, it.body, it.is_public, it.pinned, now() - make_interval(days => it.days_ago)
FROM iam.institutions i
CROSS JOIN items it
WHERE i.slug LIKE 'demo-%';
