ALTER TABLE academic.grades DROP CONSTRAINT grades_level_id_fkey,
    ADD CONSTRAINT grades_level_id_fkey FOREIGN KEY (level_id) REFERENCES academic.levels (id) ON DELETE RESTRICT;

ALTER TABLE academic.sections DROP CONSTRAINT sections_level_id_fkey,
    ADD CONSTRAINT sections_level_id_fkey FOREIGN KEY (level_id) REFERENCES academic.levels (id) ON DELETE RESTRICT;

ALTER TABLE academic.sections DROP CONSTRAINT sections_grade_id_fkey,
    ADD CONSTRAINT sections_grade_id_fkey FOREIGN KEY (grade_id) REFERENCES academic.grades (id) ON DELETE RESTRICT;
