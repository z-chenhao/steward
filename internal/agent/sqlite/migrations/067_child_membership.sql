-- Preserve exact full-spec inheritance while building the parent membership
-- set once using a compound query, instead of a correlated scan per child. NULL children
-- cannot match; NULL parent entries cannot authorize an unmatched child.
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
 AND NOT EXISTS(SELECT child.value FROM json_each(cc.tools_json) child EXCEPT SELECT parent.value FROM json_each(pc.tools_json) parent WHERE parent.value IS NOT NULL)
 AND NOT EXISTS(SELECT 1 FROM json_each(cc.tools_json) child WHERE (json_extract(child.value,'$.permission') NOT IN ('read_only','external_effect','artifact_write','spawn_agents') AND NOT (json_extract(child.value,'$.name')='builtin__memory_manage' AND json_extract(child.value,'$.permission')='memory_control' AND json_extract(child.value,'$.version')>=2)) OR json_extract(child.value,'$.name') GLOB 'builtin__task_*')
) BEGIN SELECT RAISE(ABORT,'invalid child inheritance'); END;
