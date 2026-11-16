-- DRE compliance of the CEBA demo (docs/ceba/PLAN.md, phase E8): the virtual tutoring room on the second floor, its
-- four devices, a connectivity test and an access test, and the data of both reports. The rows of the matrices are
-- created on first read and stay pending until the technical lead uploads the evidence and verifies them.
-- Fictitious data
WITH inst AS (SELECT id FROM iam.institutions WHERE slug = 'demo-eba')
INSERT INTO equipment.rooms (institution_id, name, location, area_m2, workstations, furniture, power_supply, notes)
SELECT inst.id, 'Sala de tutoría virtual', 'Segundo piso, ambiente 201', 24.5, 4,
    'cuatro escritorios con separadores, sillas ergonómicas, una pizarra acrílica y un estante para equipos',
    'tomacorrientes con línea a tierra en cada puesto y un estabilizador de 1 kVA',
    'Ventilación natural y luz LED'
FROM inst
ON CONFLICT DO NOTHING;

WITH inst AS (SELECT id FROM iam.institutions WHERE slug = 'demo-eba'),
room AS (SELECT r.id, r.institution_id FROM equipment.rooms r JOIN inst ON inst.id = r.institution_id WHERE r.name = 'Sala de tutoría virtual'),
tutor AS (SELECT st.id FROM people.staff st JOIN inst ON inst.id = st.institution_id WHERE st.code = 'P-0117')
INSERT INTO equipment.devices (institution_id, code, kind, brand, model, specs, room_id, purpose, staff_id, assigned_to, status)
SELECT room.institution_id, d.code, d.kind, d.brand, d.model, d.specs, room.id, d.purpose, CASE WHEN d.tutor THEN (SELECT id FROM tutor) END, d.assigned, d.status
FROM room, (VALUES
    ('PC-01', 'desktop', 'Lenovo', 'ThinkCentre M70s', 'Intel Core i5, 16 GB RAM, SSD 512 GB, cámara web y audífonos con micrófono', 'virtual_tutoring', true, '', 'operational'),
    ('PC-02', 'desktop', 'Lenovo', 'ThinkCentre M70s', 'Intel Core i5, 16 GB RAM, SSD 512 GB, cámara web y audífonos con micrófono', 'virtual_tutoring', false, 'Tutores de 3.° y 4.° del ciclo avanzado', 'operational'),
    ('LAP-01', 'laptop', 'HP', 'ProBook 450 G10', 'Intel Core i5, 8 GB RAM, SSD 256 GB', 'student_support', false, 'Préstamo y soporte a estudiantes', 'operational'),
    ('RTR-01', 'network', 'TP-Link', 'Archer AX55', 'Router Wi-Fi 6 y switch de 8 puertos', 'shared', false, 'Red de la sala', 'operational')
) AS d(code, kind, brand, model, specs, purpose, tutor, assigned, status)
ON CONFLICT DO NOTHING;

WITH inst AS (SELECT id FROM iam.institutions WHERE slug = 'demo-eba'),
room AS (SELECT r.id, r.institution_id FROM equipment.rooms r JOIN inst ON inst.id = r.institution_id WHERE r.name = 'Sala de tutoría virtual'),
pc AS (SELECT d.id FROM equipment.devices d JOIN inst ON inst.id = d.institution_id WHERE d.code = 'PC-01')
INSERT INTO equipment.checks (institution_id, kind, room_id, device_id, checked_on, provider, link_kind, download_mbps, upload_mbps, latency_ms, profile, passed, result, checked_by_name)
SELECT room.institution_id, 'connectivity', room.id, NULL, (now() AT TIME ZONE 'America/Lima')::date - 3, 'Movistar Fibra', 'fiber', 196.4, 98.2, 12, '', true,
    'Prueba de velocidad desde la red de la sala: conexión estable', 'Responsable técnico de la demo' FROM room
UNION ALL
SELECT room.institution_id, 'platform_access', NULL, pc.id, (now() AT TIME ZONE 'America/Lima')::date - 3, '', '', NULL, NULL, NULL, 'tutor virtual', true,
    'Ingreso al panel, apertura del aula virtual y de la tutoría virtual sin errores', 'Responsable técnico de la demo' FROM room, pc;

WITH inst AS (SELECT id FROM iam.institutions WHERE slug = 'demo-eba')
INSERT INTO compliance.reports (institution_id, kind, number, file_number, city, location, updated_by_name)
SELECT inst.id, k.kind, k.number, '2026-0013636', 'Ayacucho',
    'Asociación Señor de los Milagros, Mz. L, Lt. 1, distrito de Carmen Alto, provincia de Huamanga, Ayacucho', 'Responsable técnico de la demo'
FROM inst, (VALUES ('platform', '001-2026'), ('equipment', '002-2026')) AS k(kind, number)
ON CONFLICT DO NOTHING;
