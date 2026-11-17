-- Only a fully validated pure title is accepted here. A cancelled computation
-- leaves no pending effect to reconcile; Task creation keeps its original
-- transaction and idempotent receipts.
CREATE TABLE task_label_plan (
 invocation_id TEXT PRIMARY KEY REFERENCES tool_invocation(invocation_id),
 graph_revision TEXT NOT NULL REFERENCES rsi_graph(revision),
 request_hash TEXT NOT NULL,
 title TEXT CHECK(title IS NULL OR length(title) BETWEEN 1 AND 80)
) STRICT;
-- Forget may erase private title text, but cannot replace or revive a choice.
CREATE TRIGGER task_label_plan_immutable BEFORE UPDATE ON task_label_plan
 WHEN NEW.invocation_id IS NOT OLD.invocation_id OR NEW.graph_revision IS NOT OLD.graph_revision
 OR NEW.request_hash IS NOT OLD.request_hash OR OLD.title IS NULL OR NEW.title IS NOT NULL
 BEGIN SELECT RAISE(ABORT,'immutable task label'); END;
CREATE TRIGGER task_label_plan_retained BEFORE DELETE ON task_label_plan
 BEGIN SELECT RAISE(ABORT,'retain task label'); END;
CREATE TRIGGER task_label_plan_no_replace BEFORE INSERT ON task_label_plan
 WHEN EXISTS(SELECT 1 FROM task_label_plan WHERE invocation_id=NEW.invocation_id)
 BEGIN SELECT RAISE(ABORT,'task label exists'); END;
