-- Only identity and a frozen bounded method live here. Actual terminal facts
-- remain in the original ToolInvocation; a plan is never a passing check.
CREATE TABLE coding_check_plan (
 invocation_id TEXT PRIMARY KEY REFERENCES tool_invocation(invocation_id),
 graph_revision TEXT NOT NULL REFERENCES rsi_graph(revision),
 candidate_sha256 TEXT NOT NULL CHECK(length(candidate_sha256)=64),
 environment_scope TEXT NOT NULL,
 driver_hash TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 timeout_seconds INTEGER NOT NULL CHECK(timeout_seconds BETWEEN 1 AND 120),
 output_bytes INTEGER NOT NULL CHECK(output_bytes BETWEEN 1 AND 48000)
) STRICT;
CREATE TRIGGER coding_check_plan_immutable BEFORE UPDATE ON coding_check_plan BEGIN SELECT RAISE(ABORT,'immutable check plan'); END;
CREATE TRIGGER coding_check_plan_retained BEFORE DELETE ON coding_check_plan BEGIN SELECT RAISE(ABORT,'retain check plan'); END;
CREATE TRIGGER coding_check_plan_no_replace BEFORE INSERT ON coding_check_plan WHEN EXISTS(SELECT 1 FROM coding_check_plan WHERE invocation_id=NEW.invocation_id) BEGIN SELECT RAISE(ABORT,'check plan exists'); END;
