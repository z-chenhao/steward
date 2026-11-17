-- This freezes a pure resource recipe before the original shell effect.
-- It is not an outcome or permission to replay an uncertain command.
CREATE TABLE shell_method_plan (
 invocation_id TEXT PRIMARY KEY REFERENCES tool_invocation(invocation_id),
 graph_revision TEXT NOT NULL REFERENCES rsi_graph(revision),
 driver_hash TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 timeout_seconds INTEGER NOT NULL CHECK(timeout_seconds BETWEEN 1 AND 120),
 output_bytes INTEGER NOT NULL CHECK(output_bytes BETWEEN 1 AND 48000)
) STRICT;
CREATE TRIGGER shell_method_plan_immutable BEFORE UPDATE ON shell_method_plan BEGIN SELECT RAISE(ABORT,'immutable shell method'); END;
CREATE TRIGGER shell_method_plan_retained BEFORE DELETE ON shell_method_plan BEGIN SELECT RAISE(ABORT,'retain shell method'); END;
CREATE TRIGGER shell_method_plan_no_replace BEFORE INSERT ON shell_method_plan WHEN EXISTS(SELECT 1 FROM shell_method_plan WHERE invocation_id=NEW.invocation_id) BEGIN SELECT RAISE(ABORT,'shell method exists'); END;
