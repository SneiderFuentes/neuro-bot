-- Script para insertar configuración de citas para fisiatría
-- Doctores: 72199429, 7178922
-- Agendas: 733, 736, 739, 749 (de tblexepciondias para doctor 72199429)

-- Insertar configuración para doctor 72199429 (Willington Chona Suarez)
INSERT INTO citas_conf (
    IdMedico, IdAgenda, DuracionCita, Activo, SesionesxCita,
    Trabaja0, Trabaja1, Trabaja2, Trabaja3, Trabaja4, Trabaja5, Trabaja6,
    HInicioM0, HFinalM0, HInicioT0, HFinalT0,
    HInicioM1, HFinalM1, HInicioT1, HFinalT1,
    HInicioM2, HFinalM2, HInicioT2, HFinalT2,
    HInicioM3, HFinalM3, HInicioT3, HFinalT3,
    HInicioM4, HFinalM4, HInicioT4, HFinalT4,
    HInicioM5, HFinalM5, HInicioT5, HFinalT5,
    HInicioM6, HFinalM6, HInicioT6, HFinalT6
) VALUES 
-- Agenda 733 - Doctor 72199429
('72199429', 733, 30, 1, 1,
 0, 1, 1, 1, 1, 1, 0,  -- Lunes a Viernes
 '07:00', '12:00', '14:00', '18:00',  -- Domingo (no trabaja)
 '07:00', '12:00', '14:00', '18:00',  -- Lunes
 '07:00', '12:00', '14:00', '18:00',  -- Martes
 '07:00', '12:00', '14:00', '18:00',  -- Miércoles
 '07:00', '12:00', '14:00', '18:00',  -- Jueves
 '07:00', '12:00', '14:00', '18:00',  -- Viernes
 '07:00', '12:00', '', ''  -- Sábado (solo mañana)
),
-- Agenda 736 - Doctor 72199429
('72199429', 736, 30, 1, 1,
 0, 1, 1, 1, 1, 1, 0,
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '', ''
),
-- Agenda 739 - Doctor 72199429
('72199429', 739, 30, 1, 1,
 0, 1, 1, 1, 1, 1, 0,
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '', ''
),
-- Agenda 749 - Doctor 72199429
('72199429', 749, 30, 1, 1,
 0, 1, 1, 1, 1, 1, 0,
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '14:00', '18:00',
 '07:00', '12:00', '', ''
);

-- Nota: Este script asume horarios estándar (7am-12pm y 2pm-6pm)
-- Ajusta los horarios según la realidad de cada agenda
-- Si ya existen registros, este script fallará por claves duplicadas
