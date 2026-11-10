ALTER TABLE comms.announcement_attachments DROP CONSTRAINT announcement_attachments_position_key;
ALTER TABLE comms.announcement_attachments ADD CONSTRAINT announcement_attachments_position_key UNIQUE (announcement_id, position);

DROP INDEX IF EXISTS comms.idx_comms_announcements_featured;
ALTER TABLE comms.announcements
    DROP CONSTRAINT announcements_cta_url_format,
    DROP CONSTRAINT announcements_cta_needs_window,
    DROP CONSTRAINT announcements_cta_label_check,
    DROP CONSTRAINT announcements_featured_public,
    DROP CONSTRAINT announcements_featured_window,
    DROP COLUMN cta_url,
    DROP COLUMN cta_label,
    DROP COLUMN featured_until,
    DROP COLUMN featured_from;
