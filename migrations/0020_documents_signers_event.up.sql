-- History line for a change of signers on a pending version (people added or removed before it was published)
ALTER TABLE documents.events DROP CONSTRAINT events_action_check,
    ADD CONSTRAINT events_action_check CHECK (action IN ('generated', 'signed', 'published', 'superseded', 'cancelled', 'signers'));
