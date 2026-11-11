-- Removing an institution must take its whole academic structure with it. Grades go with their level (the API still
-- refuses to delete a level that has grades). Sections check their level and grade at commit (DEFERRABLE INITIALLY
-- DEFERRED): when the institution goes, the cascade through its periods has removed them by then; deleting a level or
-- grade that still has sections fails as before
ALTER TABLE academic.grades DROP CONSTRAINT grades_level_id_fkey,
    ADD CONSTRAINT grades_level_id_fkey FOREIGN KEY (level_id) REFERENCES academic.levels (id) ON DELETE CASCADE;

ALTER TABLE academic.sections DROP CONSTRAINT sections_level_id_fkey,
    ADD CONSTRAINT sections_level_id_fkey FOREIGN KEY (level_id) REFERENCES academic.levels (id) ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE academic.sections DROP CONSTRAINT sections_grade_id_fkey,
    ADD CONSTRAINT sections_grade_id_fkey FOREIGN KEY (grade_id) REFERENCES academic.grades (id) ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED;
