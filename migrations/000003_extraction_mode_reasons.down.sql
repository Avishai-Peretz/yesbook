ALTER TABLE processing_events
    DROP CONSTRAINT IF EXISTS processing_events_reason_code_len;

ALTER TABLE processing_events
    DROP COLUMN IF EXISTS reason_code;

ALTER TABLE attempts
    DROP CONSTRAINT IF EXISTS attempts_reason_message_len;

ALTER TABLE attempts
    DROP CONSTRAINT IF EXISTS attempts_decision_check;

ALTER TABLE attempts
    DROP COLUMN IF EXISTS decision,
    DROP COLUMN IF EXISTS reason_message,
    DROP COLUMN IF EXISTS reason_code;

ALTER TABLE jobs
    DROP CONSTRAINT IF EXISTS jobs_extraction_mode_check;

ALTER TABLE jobs
    DROP COLUMN IF EXISTS extraction_mode;
