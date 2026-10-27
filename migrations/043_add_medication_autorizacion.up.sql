ALTER TABLE medication_pending
    ADD COLUMN numero_autorizacion VARCHAR(100) NOT NULL DEFAULT '' AFTER fecha_vencimiento;
