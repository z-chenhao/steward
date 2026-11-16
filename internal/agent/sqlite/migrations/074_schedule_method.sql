-- Only newly accepted managed schedule edits bind a method graph. Existing
-- plans keep their original native interpretation; no historical graph is made up.
ALTER TABLE automation ADD COLUMN method_graph TEXT REFERENCES rsi_graph(revision);
CREATE TRIGGER automation_method_graph_version BEFORE UPDATE ON automation
 WHEN NEW.method_graph IS NOT OLD.method_graph
  AND NEW.schedule_version<=OLD.schedule_version
 BEGIN SELECT RAISE(ABORT,'schedule graph requires a new schedule version'); END;

-- A later edit may select another policy. The accepted fire retains its own
-- immutable provenance rather than following the mutable current plan.
ALTER TABLE automation_fire ADD COLUMN method_graph TEXT REFERENCES rsi_graph(revision);
CREATE TRIGGER automation_fire_method_immutable BEFORE UPDATE ON automation_fire
 WHEN NEW.method_graph IS NOT OLD.method_graph
 BEGIN SELECT RAISE(ABORT,'immutable fire policy binding'); END;
