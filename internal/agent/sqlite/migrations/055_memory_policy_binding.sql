-- The intent records migration participation at creation. Method selection is
-- pinned with the intent when available; a disabled intent can wait for one
-- explicit activation before private preparation, without a surrogate Run.
CREATE TABLE memory_policy_binding (
 event_id TEXT PRIMARY KEY REFERENCES memory_mutation(event_id),
 managed INTEGER NOT NULL CHECK(managed IN(0,1)),
 graph_revision TEXT REFERENCES rsi_graph(revision),
 CHECK(managed=1 OR graph_revision IS NULL)
) STRICT;
INSERT INTO memory_policy_binding SELECT event_id,0,NULL FROM memory_mutation;
CREATE TRIGGER memory_policy_binding_once BEFORE UPDATE ON memory_policy_binding
 WHEN NEW.event_id!=OLD.event_id OR NEW.managed!=OLD.managed OR OLD.managed!=1
 OR OLD.graph_revision IS NOT NULL OR NEW.graph_revision IS NULL
 BEGIN SELECT RAISE(ABORT,'immutable memory policy selection'); END;
CREATE TRIGGER memory_policy_binding_no_delete BEFORE DELETE ON memory_policy_binding
 BEGIN SELECT RAISE(ABORT,'retained memory policy selection'); END;
CREATE TRIGGER memory_policy_binding_no_replace BEFORE INSERT ON memory_policy_binding
 WHEN EXISTS(SELECT 1 FROM memory_policy_binding WHERE event_id=NEW.event_id)
 BEGIN SELECT RAISE(ABORT,'memory policy selection exists'); END;
