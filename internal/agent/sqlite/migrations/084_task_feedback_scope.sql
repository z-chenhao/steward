-- Existing questions keep their original Task-wide replacement semantics.
ALTER TABLE task_feedback ADD COLUMN separate_kinds INTEGER NOT NULL DEFAULT 0 CHECK(separate_kinds IN (0,1));

DROP INDEX task_feedback_pending;
CREATE UNIQUE INDEX task_feedback_pending ON task_feedback(task_id,kind) WHERE status='pending';

CREATE TRIGGER task_feedback_scope_immutable BEFORE UPDATE OF separate_kinds ON task_feedback
 WHEN NEW.separate_kinds!=OLD.separate_kinds
 BEGIN SELECT RAISE(ABORT,'immutable feedback scope'); END;
DROP TRIGGER task_feedback_no_replace;
CREATE TRIGGER task_feedback_no_replace BEFORE INSERT ON task_feedback WHEN EXISTS(
 SELECT 1 FROM task_feedback WHERE feedback_ref=NEW.feedback_ref OR
 (task_id=NEW.task_id AND status='pending' AND NEW.status='pending'
  AND (kind=NEW.kind OR separate_kinds=0 OR NEW.separate_kinds=0)))
 BEGIN SELECT RAISE(ABORT,'task feedback identity exists'); END;
