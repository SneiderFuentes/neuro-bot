ALTER TABLE waiting_list ADD COLUMN procedure_type VARCHAR(50) NOT NULL DEFAULT '' AFTER procedures_json;
