-- A Task's first complete import choice survives later Root/plugin versions.
CREATE TABLE code_import_policy_binding (
 task_id TEXT PRIMARY KEY REFERENCES task_code_environment(task_id),
 managed INTEGER NOT NULL CHECK(managed IN (0,1)),
 scope_hash TEXT NOT NULL,
 graph_revision TEXT REFERENCES rsi_graph(revision),
 driver_hash TEXT,
 limits_json TEXT CHECK(limits_json IS NULL OR json_valid(limits_json)),
 CHECK((managed=0 AND graph_revision IS NULL AND driver_hash IS NULL AND limits_json IS NULL) OR (managed=1 AND ((graph_revision IS NULL AND driver_hash IS NULL AND limits_json IS NULL) OR (graph_revision IS NOT NULL AND driver_hash IS NOT NULL AND limits_json IS NOT NULL))))
) STRICT;
INSERT INTO code_import_policy_binding(task_id,managed,scope_hash) SELECT task_id,0,scope_hash FROM task_code_environment;
CREATE TRIGGER code_import_policy_immutable BEFORE UPDATE ON code_import_policy_binding WHEN NEW.task_id IS NOT OLD.task_id OR NEW.managed IS NOT OLD.managed OR NEW.scope_hash IS NOT OLD.scope_hash OR OLD.graph_revision IS NOT NULL OR OLD.managed=0 OR NEW.graph_revision IS NULL BEGIN SELECT RAISE(ABORT,'immutable import method'); END;
CREATE TRIGGER code_import_policy_retained BEFORE DELETE ON code_import_policy_binding BEGIN SELECT RAISE(ABORT,'retain import method'); END;
CREATE TRIGGER code_import_policy_no_replace BEFORE INSERT ON code_import_policy_binding WHEN EXISTS(SELECT 1 FROM code_import_policy_binding WHERE task_id=NEW.task_id) BEGIN SELECT RAISE(ABORT,'import method exists'); END;
CREATE TABLE code_report_method_binding (
 artifact_ref TEXT PRIMARY KEY REFERENCES artifact(artifact_ref),
 graph_revision TEXT NOT NULL REFERENCES rsi_graph(revision),
 driver_hash TEXT NOT NULL,
 recipe_json TEXT NOT NULL CHECK(json_valid(recipe_json))
) STRICT;
CREATE TRIGGER code_report_method_immutable BEFORE UPDATE ON code_report_method_binding BEGIN SELECT RAISE(ABORT,'immutable report method'); END;
CREATE TRIGGER code_report_method_retained BEFORE DELETE ON code_report_method_binding BEGIN SELECT RAISE(ABORT,'retain report method'); END;
CREATE TRIGGER code_report_method_no_replace BEFORE INSERT ON code_report_method_binding WHEN EXISTS(SELECT 1 FROM code_report_method_binding WHERE artifact_ref=NEW.artifact_ref) BEGIN SELECT RAISE(ABORT,'report method exists'); END;
