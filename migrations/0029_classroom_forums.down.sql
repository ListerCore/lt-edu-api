DROP TABLE IF EXISTS classroom.forum_reads;
DROP TABLE IF EXISTS classroom.posts;
DROP TABLE IF EXISTS classroom.forums;
DELETE FROM classroom.items WHERE kind = 'forum';
ALTER TABLE classroom.items DROP CONSTRAINT items_kind_check;
ALTER TABLE classroom.items ADD CONSTRAINT items_kind_check CHECK (kind IN ('reading', 'file', 'video', 'link', 'live', 'activity', 'quiz', 'task'));
