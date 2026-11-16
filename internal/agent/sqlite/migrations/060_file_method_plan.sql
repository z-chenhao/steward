-- An accepted pure method precedes the original, never-replayed file effect.
-- It is a choice receipt, not proof that the filesystem mutation completed.
CREATE TABLE file_method_plan (
 invocation_id TEXT PRIMARY KEY REFERENCES tool_invocation(invocation_id),
 graph_revision TEXT NOT NULL REFERENCES rsi_graph(revision),
 driver_hash TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 source_hash TEXT,
 span_start INTEGER CHECK(span_start IS NULL OR span_start>=0)
) STRICT;
CREATE TRIGGER file_method_plan_immutable BEFORE UPDATE ON file_method_plan BEGIN SELECT RAISE(ABORT,'immutable file method'); END;
CREATE TRIGGER file_method_plan_retained BEFORE DELETE ON file_method_plan BEGIN SELECT RAISE(ABORT,'retain file method'); END;
CREATE TRIGGER file_method_plan_no_replace BEFORE INSERT ON file_method_plan WHEN EXISTS(SELECT 1 FROM file_method_plan WHERE invocation_id=NEW.invocation_id) BEGIN SELECT RAISE(ABORT,'file method exists'); END;
