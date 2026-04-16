ALTER TABLE waiting_list
    MODIFY COLUMN status ENUM('waiting','notified','scheduled','declined','expired','duplicate_found') DEFAULT 'waiting';
