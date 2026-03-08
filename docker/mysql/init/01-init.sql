-- Asegurar que la BD existe con charset correcto
CREATE DATABASE IF NOT EXISTS neuro_bot CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
GRANT ALL PRIVILEGES ON neuro_bot.* TO 'botuser'@'%';
FLUSH PRIVILEGES;

USE neuro_bot;

-- Tablas necesarias para el seed (02-seed-data.sql).
-- El resto las crea la app con migraciones (golang-migrate).
-- Usar IF NOT EXISTS para no conflictar con la migración 007.
CREATE TABLE IF NOT EXISTS center_locations (
    id INT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(200) NOT NULL,
    address VARCHAR(500) NOT NULL,
    phone VARCHAR(20) NOT NULL DEFAULT '',
    google_maps_url VARCHAR(500) NOT NULL DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
