-- Freeze title provenance only when this fire actually creates a Task.
-- Historical fires and later fires reusing an existing Task remain NULL.
ALTER TABLE automation_fire ADD COLUMN task_label_managed INTEGER CHECK(task_label_managed IS NULL OR task_label_managed IN (0,1));
ALTER TABLE automation_fire ADD COLUMN task_label_graph TEXT REFERENCES rsi_graph(revision);
ALTER TABLE automation_fire ADD COLUMN task_title TEXT;
CREATE TRIGGER automation_task_label_immutable BEFORE UPDATE ON automation_fire
 WHEN OLD.task_title IS NOT NULL AND (NEW.task_title IS NOT OLD.task_title OR NEW.task_label_graph IS NOT OLD.task_label_graph OR NEW.task_label_managed IS NOT OLD.task_label_managed)
 BEGIN SELECT RAISE(ABORT,'immutable accepted automation task title'); END;
