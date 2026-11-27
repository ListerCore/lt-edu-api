-- Optional modules an institution turned off (RF01). Every module of the institution kind is on unless it is listed
-- here, so a new institution starts with the whole set of its kind and no rows
CREATE TABLE iam.disabled_modules (
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    module         varchar(40) NOT NULL,
    disabled_by    uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    disabled_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (institution_id, module)
);
