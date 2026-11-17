-- Pure view output is accepted atomically with its UI artifact. No pending
-- effect is reserved for a cancelled or interrupted read-only computation.
CREATE TABLE ui_table_view_snapshot (
 invocation_id TEXT PRIMARY KEY REFERENCES ui_widget(invocation_id),
 graph_revision TEXT NOT NULL REFERENCES rsi_graph(revision),
 result_json TEXT NOT NULL CHECK(json_valid(result_json))
) STRICT;
CREATE TRIGGER ui_table_view_snapshot_immutable BEFORE UPDATE ON ui_table_view_snapshot
 BEGIN SELECT RAISE(ABORT,'immutable UI table view'); END;
CREATE TRIGGER ui_table_view_snapshot_retained BEFORE DELETE ON ui_table_view_snapshot
 BEGIN SELECT RAISE(ABORT,'retain UI table view'); END;
CREATE TRIGGER ui_table_view_snapshot_no_replace BEFORE INSERT ON ui_table_view_snapshot
 WHEN EXISTS(SELECT 1 FROM ui_table_view_snapshot WHERE invocation_id=NEW.invocation_id)
 BEGIN SELECT RAISE(ABORT,'UI table view exists'); END;
