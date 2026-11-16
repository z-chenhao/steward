-- Complete pure parameter choices are frozen before the original approval.
-- No pending effect / generic RSI Call is created by preparation.
CREATE TABLE computer_action_plan (
 invocation_id TEXT PRIMARY KEY REFERENCES tool_invocation(invocation_id),
 graph_revision TEXT NOT NULL REFERENCES rsi_graph(revision),
 request_hash TEXT NOT NULL,
 method TEXT NOT NULL,
 surface_kind TEXT NOT NULL CHECK(surface_kind IN ('','native','tab')),
 plan_hash TEXT NOT NULL,
 plan_json TEXT CHECK(plan_json IS NULL OR json_valid(plan_json) AND length(plan_json)<=1048576)
) STRICT;
-- Forget can erase private parameters, without replacing or reviving a plan.
CREATE TRIGGER computer_action_plan_immutable BEFORE UPDATE ON computer_action_plan
 WHEN NEW.invocation_id IS NOT OLD.invocation_id OR NEW.graph_revision IS NOT OLD.graph_revision
 OR NEW.request_hash IS NOT OLD.request_hash OR NEW.method IS NOT OLD.method
 OR NEW.surface_kind IS NOT OLD.surface_kind OR NEW.plan_hash IS NOT OLD.plan_hash
 OR OLD.plan_json IS NULL OR NEW.plan_json IS NOT NULL
 BEGIN SELECT RAISE(ABORT,'immutable Computer action'); END;
CREATE TRIGGER computer_action_plan_retained BEFORE DELETE ON computer_action_plan
 BEGIN SELECT RAISE(ABORT,'retain Computer action'); END;
CREATE TRIGGER computer_action_plan_no_replace BEFORE INSERT ON computer_action_plan
 WHEN EXISTS(SELECT 1 FROM computer_action_plan WHERE invocation_id=NEW.invocation_id)
 BEGIN SELECT RAISE(ABORT,'Computer action exists'); END;
