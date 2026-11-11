DELETE FROM documents.events WHERE action = 'signers';
ALTER TABLE documents.events DROP CONSTRAINT events_action_check,
    ADD CONSTRAINT events_action_check CHECK (action IN ('generated', 'signed', 'published', 'superseded', 'cancelled'));
