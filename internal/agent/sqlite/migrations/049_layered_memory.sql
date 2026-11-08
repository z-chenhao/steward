-- Document bodies stay in the Memory adapter; this database owns execution.
CREATE TABLE run_injected_memory(run_id TEXT PRIMARY KEY REFERENCES run(run_id),privacy_epoch INTEGER NOT NULL,content_sealed TEXT NOT NULL,created_at INTEGER NOT NULL) STRICT;
CREATE TRIGGER run_injected_memory_immutable BEFORE UPDATE ON run_injected_memory BEGIN SELECT RAISE(ABORT,'immutable injected memory'); END;
CREATE TRIGGER run_injected_memory_audit BEFORE DELETE ON run_injected_memory BEGIN SELECT RAISE(ABORT,'retain injected memory'); END;
CREATE TABLE memory_dream_step(event_id TEXT NOT NULL REFERENCES memory_mutation(event_id),pass_index INTEGER NOT NULL,status TEXT NOT NULL CHECK(status IN('pending','completed','failed')),finished INTEGER NOT NULL DEFAULT 0 CHECK(finished IN(0,1)),PRIMARY KEY(event_id,pass_index)) STRICT;
ALTER TABLE memory_generation ADD COLUMN purpose TEXT NOT NULL DEFAULT 'facts' CHECK(purpose IN('facts','dream'));
ALTER TABLE memory_generation ADD COLUMN pass_index INTEGER NOT NULL DEFAULT 0;
CREATE TABLE memory_dream_coverage(agent_id TEXT PRIMARY KEY,through_sequence INTEGER NOT NULL CHECK(through_sequence>=0),next_offset INTEGER NOT NULL DEFAULT 0 CHECK(next_offset>=0)) STRICT;

-- Document operations inherit the frozen tool; Host still denies user controls.
DROP TRIGGER IF EXISTS child_initial;
CREATE TRIGGER child_initial BEFORE INSERT ON run WHEN NEW.worker_position='child' AND NOT EXISTS(
 SELECT 1 FROM run p JOIN capability_snapshot pc ON pc.snapshot_id=p.capability_snapshot_id
 JOIN capability_snapshot cc ON cc.snapshot_id=NEW.capability_snapshot_id
 WHERE p.run_id=NEW.parent_run_id AND p.kind='worker' AND p.status='running' AND p.desired_state='running'
 AND p.task_id=NEW.task_id AND p.root_run_id=NEW.root_run_id AND p.task_epoch=NEW.task_epoch
 AND p.agent_id=NEW.agent_id AND p.conversation_id=NEW.conversation_id AND NEW.depth=p.depth+1
 AND p.model_snapshot_id=NEW.model_snapshot_id AND pc.can_spawn_agents=1
 AND cc.role='worker' AND cc.data_scope=pc.data_scope AND cc.agent_config_version=pc.agent_config_version
 AND cc.can_spawn_agents<=pc.can_spawn_agents AND cc.format_version=pc.format_version
 AND NOT EXISTS(SELECT 1 FROM json_each(cc.tools_json) child WHERE NOT EXISTS(SELECT 1 FROM json_each(pc.tools_json) parent WHERE child.value=parent.value))
 AND NOT EXISTS(SELECT 1 FROM json_each(cc.tools_json) child WHERE (json_extract(child.value,'$.permission') NOT IN ('read_only','external_effect','artifact_write','spawn_agents') AND NOT (json_extract(child.value,'$.name')='builtin__memory_manage' AND json_extract(child.value,'$.permission')='memory_control' AND json_extract(child.value,'$.version')>=2)) OR json_extract(child.value,'$.name') GLOB 'builtin__task_*')
) BEGIN SELECT RAISE(ABORT,'invalid child inheritance'); END;
