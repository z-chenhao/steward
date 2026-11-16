-- A complete pure recipe is accepted before an external Tool effect. Recovery
-- reads this immutable fact; it never asks a current plugin how to replay an
-- effect. No user/model body or arbitrary mutable state is stored here.
-- Preserve only actual preexisting owner consumers. An empty new installation
-- cannot acquire a silent native fallback by lacking the current bundle.
CREATE TABLE legacy_artifact_owner (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 control_epoch INTEGER NOT NULL CHECK(control_epoch>=0),
 run_count INTEGER NOT NULL CHECK(run_count>=0),
 artifact_count INTEGER NOT NULL CHECK(artifact_count>=0),
 CHECK(control_epoch>0 OR run_count>0 OR artifact_count>0)
) STRICT;
INSERT INTO legacy_artifact_owner SELECT 1,(SELECT epoch FROM rsi_control WHERE singleton=1),
 (SELECT count(*) FROM run),(SELECT count(*) FROM artifact)
 WHERE (SELECT epoch FROM rsi_control WHERE singleton=1)>0
 OR EXISTS(SELECT 1 FROM run) OR EXISTS(SELECT 1 FROM artifact);
CREATE TRIGGER legacy_artifact_owner_immutable BEFORE UPDATE ON legacy_artifact_owner
 BEGIN SELECT RAISE(ABORT,'immutable legacy artifact owner'); END;
CREATE TRIGGER legacy_artifact_owner_retained BEFORE DELETE ON legacy_artifact_owner
 BEGIN SELECT RAISE(ABORT,'retain legacy artifact owner'); END;
CREATE TRIGGER legacy_artifact_owner_no_replace BEFORE INSERT ON legacy_artifact_owner
 BEGIN SELECT RAISE(ABORT,'legacy artifact owner is migration-only'); END;

ALTER TABLE artifact ADD COLUMN method_graph TEXT REFERENCES rsi_graph(revision);
ALTER TABLE artifact ADD COLUMN semantic_kind TEXT CHECK(
 (semantic_kind IS NULL)=(method_graph IS NULL) AND
 (semantic_kind IS NULL OR semantic_kind IN ('file','image','audio','video')));
CREATE TRIGGER artifact_semantic_immutable BEFORE UPDATE ON artifact
 WHEN NEW.method_graph IS NOT OLD.method_graph OR NEW.semantic_kind IS NOT OLD.semantic_kind
 BEGIN SELECT RAISE(ABORT,'immutable artifact semantics'); END;

CREATE TABLE run_artifact_request (
 run_id TEXT PRIMARY KEY REFERENCES run(run_id),
 retention_days INTEGER NOT NULL CHECK(retention_days BETWEEN 0 AND 3650),
 UNIQUE(run_id,retention_days)
) STRICT;
CREATE TRIGGER run_artifact_request_immutable BEFORE UPDATE ON run_artifact_request
 BEGIN SELECT RAISE(ABORT,'immutable artifact request'); END;
CREATE TRIGGER run_artifact_request_retained BEFORE DELETE ON run_artifact_request
 BEGIN SELECT RAISE(ABORT,'retain artifact request'); END;
CREATE TRIGGER run_artifact_request_no_replace BEFORE INSERT ON run_artifact_request
 WHEN EXISTS(SELECT 1 FROM run_artifact_request WHERE run_id=NEW.run_id)
 BEGIN SELECT RAISE(ABORT,'artifact request exists'); END;
CREATE TABLE run_artifact_recipe (
 run_id TEXT PRIMARY KEY REFERENCES run(run_id),
 graph_revision TEXT NOT NULL REFERENCES rsi_graph(revision),
 requested_retention_days INTEGER NOT NULL CHECK(requested_retention_days BETWEEN 0 AND 3650),
 retention_days INTEGER NOT NULL CHECK(retention_days BETWEEN 1 AND 3650
   AND (requested_retention_days=0 OR retention_days=requested_retention_days)),
 media_kinds_json TEXT NOT NULL CHECK(json_valid(media_kinds_json)
   AND json_type(media_kinds_json)='array' AND json_array_length(media_kinds_json)<=3),
 overflow_name TEXT NOT NULL CHECK(length(overflow_name) BETWEEN 1 AND 255),
 overflow_media TEXT NOT NULL CHECK(length(overflow_media) BETWEEN 1 AND 128),
 stored_text TEXT NOT NULL CHECK(length(stored_text) BETWEEN 1 AND 1024),
 publish_text TEXT NOT NULL CHECK(length(publish_text) BETWEEN 1 AND 1024),
 FOREIGN KEY(run_id,requested_retention_days) REFERENCES run_artifact_request(run_id,retention_days)
) STRICT;
CREATE TRIGGER run_artifact_recipe_immutable BEFORE UPDATE ON run_artifact_recipe
 BEGIN SELECT RAISE(ABORT,'immutable artifact recipe'); END;
CREATE TRIGGER run_artifact_recipe_retained BEFORE DELETE ON run_artifact_recipe
 BEGIN SELECT RAISE(ABORT,'retain artifact recipe'); END;
CREATE TRIGGER run_artifact_recipe_no_replace BEFORE INSERT ON run_artifact_recipe
 WHEN EXISTS(SELECT 1 FROM run_artifact_recipe WHERE run_id=NEW.run_id)
 BEGIN SELECT RAISE(ABORT,'artifact recipe exists'); END;
