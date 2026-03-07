-- Asegurar que la BD existe con charset correcto
CREATE DATABASE IF NOT EXISTS neuro_bot CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
GRANT ALL PRIVILEGES ON neuro_bot.* TO 'botuser'@'%';
FLUSH PRIVILEGES;
