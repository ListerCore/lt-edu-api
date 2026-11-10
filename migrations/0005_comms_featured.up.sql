-- Validity window ("vigencia"): while now is within it, a public announcement is featured in the modal of the
-- site's home, with a button ("Ir" / "Continuar") leading to cta_url (its own page when empty). Once it ends the
-- announcement stays on /news like any other
ALTER TABLE comms.announcements
    ADD COLUMN featured_from timestamptz,
    ADD COLUMN featured_until timestamptz,
    ADD COLUMN cta_label varchar(20),
    ADD COLUMN cta_url varchar(500);

ALTER TABLE comms.announcements
    ADD CONSTRAINT announcements_featured_window CHECK (
        (featured_from IS NULL) = (featured_until IS NULL) AND (featured_until IS NULL OR featured_until > featured_from)
    ),
    -- The modal is on the public site, so only public announcements are featured
    ADD CONSTRAINT announcements_featured_public CHECK (featured_until IS NULL OR is_public),
    ADD CONSTRAINT announcements_cta_label_check CHECK (cta_label IS NULL OR cta_label IN ('Ir', 'Continuar')),
    ADD CONSTRAINT announcements_cta_needs_window CHECK (featured_until IS NOT NULL OR (cta_label IS NULL AND cta_url IS NULL)),
    -- A path of the site (/admision) or an https address
    ADD CONSTRAINT announcements_cta_url_format CHECK (cta_url IS NULL OR cta_url ~ '^(/([^/].*)?|https://[^\s]+)$');

CREATE INDEX idx_comms_announcements_featured ON comms.announcements (institution_id, featured_until)
    WHERE featured_until IS NOT NULL;

-- Editing reorders attachments inside one transaction: positions may collide until it commits
ALTER TABLE comms.announcement_attachments DROP CONSTRAINT announcement_attachments_position_key;
ALTER TABLE comms.announcement_attachments ADD CONSTRAINT announcement_attachments_position_key
    UNIQUE (announcement_id, position) DEFERRABLE INITIALLY IMMEDIATE;
