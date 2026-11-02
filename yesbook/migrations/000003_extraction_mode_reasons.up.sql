ALTER TABLE jobs
    ADD COLUMN extraction_mode text NOT NULL DEFAULT 'automatic';

ALTER TABLE jobs
    ADD CONSTRAINT jobs_extraction_mode_check
    CHECK (extraction_mode IN ('automatic', 'pdfium', 'docling', 'gemini', 'openai'));

ALTER TABLE attempts
    ADD COLUMN reason_code text NOT NULL DEFAULT '',
    ADD COLUMN reason_message text NOT NULL DEFAULT '',
    ADD COLUMN decision text NOT NULL DEFAULT '';

ALTER TABLE attempts
    ADD CONSTRAINT attempts_decision_check
    CHECK (decision IN ('', 'chosen', 'declined', 'skipped'));

ALTER TABLE attempts
    ADD CONSTRAINT attempts_reason_message_len
    CHECK (char_length(reason_message) <= 2048);

ALTER TABLE processing_events
    ADD COLUMN reason_code text NOT NULL DEFAULT '';

ALTER TABLE processing_events
    ADD CONSTRAINT processing_events_reason_code_len
    CHECK (char_length(reason_code) <= 64);
