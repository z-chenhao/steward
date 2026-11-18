-- A Task's first approved prepared-input basis is frozen independently from
-- preparation readiness. Replacing an input basis requires a new Task.
CREATE TABLE coding_task_environment (
 task_id TEXT PRIMARY KEY REFERENCES task_code_environment(task_id),
 image_id TEXT NOT NULL,
 environment_sha256 TEXT NOT NULL CHECK(length(environment_sha256)=64),
 basis_json TEXT NOT NULL CHECK(json_valid(basis_json))
) STRICT;
CREATE TRIGGER coding_task_environment_immutable BEFORE UPDATE ON coding_task_environment BEGIN SELECT RAISE(ABORT,'immutable Task environment'); END;
CREATE TRIGGER coding_task_environment_retained BEFORE DELETE ON coding_task_environment BEGIN SELECT RAISE(ABORT,'retain Task environment'); END;
CREATE TRIGGER coding_task_environment_no_replace BEFORE INSERT ON coding_task_environment WHEN EXISTS(SELECT 1 FROM coding_task_environment WHERE task_id=NEW.task_id) BEGIN SELECT RAISE(ABORT,'Task environment exists'); END;

CREATE TABLE coding_project_plan (
 invocation_id TEXT PRIMARY KEY REFERENCES tool_invocation(invocation_id),
 graph_revision TEXT NOT NULL REFERENCES rsi_graph(revision),
 candidate_sha256 TEXT NOT NULL CHECK(length(candidate_sha256)=64),
 environment_scope TEXT NOT NULL,
 environment_sha256 TEXT NOT NULL CHECK(length(environment_sha256)=64),
 image_id TEXT NOT NULL,
 driver_hash TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 project_sha256 TEXT NOT NULL CHECK(length(project_sha256)=64),
 plan_json TEXT NOT NULL CHECK(json_valid(plan_json))
) STRICT;
CREATE TRIGGER coding_project_plan_immutable BEFORE UPDATE ON coding_project_plan BEGIN SELECT RAISE(ABORT,'immutable project plan'); END;
CREATE TRIGGER coding_project_plan_retained BEFORE DELETE ON coding_project_plan BEGIN SELECT RAISE(ABORT,'retain project plan'); END;
CREATE TRIGGER coding_project_plan_no_replace BEFORE INSERT ON coding_project_plan WHEN EXISTS(SELECT 1 FROM coding_project_plan WHERE invocation_id=NEW.invocation_id) BEGIN SELECT RAISE(ABORT,'project plan exists'); END;

-- Physical resource recovery only. It never supplies a verification outcome.
CREATE TABLE coding_project_resource (
 invocation_id TEXT PRIMARY KEY REFERENCES coding_project_plan(invocation_id),
 socket_path TEXT NOT NULL,
 directory TEXT NOT NULL,
 create_started INTEGER NOT NULL DEFAULT 0 CHECK(create_started IN (0,1)),
 creation_observed INTEGER NOT NULL DEFAULT 0 CHECK(creation_observed IN (0,1)),
 released INTEGER NOT NULL DEFAULT 0 CHECK(released IN (0,1)),
 next_cleanup_at INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE TRIGGER coding_project_resource_identity BEFORE UPDATE ON coding_project_resource
 WHEN NEW.invocation_id!=OLD.invocation_id OR NEW.socket_path!=OLD.socket_path OR NEW.directory!=OLD.directory OR NEW.create_started<OLD.create_started OR NEW.creation_observed<OLD.creation_observed OR NEW.released<OLD.released
 BEGIN SELECT RAISE(ABORT,'immutable project resource identity'); END;
CREATE TRIGGER coding_project_resource_retained BEFORE DELETE ON coding_project_resource BEGIN SELECT RAISE(ABORT,'retain project resource'); END;
CREATE TRIGGER coding_project_resource_no_replace BEFORE INSERT ON coding_project_resource WHEN EXISTS(SELECT 1 FROM coding_project_resource WHERE invocation_id=NEW.invocation_id) BEGIN SELECT RAISE(ABORT,'project resource exists'); END;
