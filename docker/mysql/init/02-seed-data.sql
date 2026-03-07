-- Seed data for neuro_bot local DB
-- Based on Bird V2 flow and center configuration

USE neuro_bot;

-- =============================================
-- Center Locations (from Bird flow V2)
-- =============================================
INSERT INTO center_locations (name, address, phone, google_maps_url, is_active) VALUES
('Sede Torre', 'Calle 35 No 36-26 - Barrio Barzal Alto', '', 'https://maps.app.goo.gl/eVNp9t7wY8DhgUhR6', TRUE),
('Sede Imagenes', 'Calle 34 No 38-47 - Barrio Barzal Alto', '', 'https://maps.app.goo.gl/MZqCxVoKAgwrnUVh7', TRUE)
ON DUPLICATE KEY UPDATE name = VALUES(name);
