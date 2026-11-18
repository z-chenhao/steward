-- Extend the two existing kind constraints without replacing the binding table,
-- whose scope is referenced by historical followups. Keep generations and CAS
-- versions unchanged while restoring the granted rule within this transaction.
CREATE TEMP TABLE saved_github_rules AS
 SELECT scope,rule_mode,rule_event_kind,rule_check_conclusion FROM github_task_binding;
DROP TRIGGER github_task_binding_version;
ALTER TABLE github_task_binding DROP COLUMN rule_check_conclusion;
ALTER TABLE github_task_binding DROP COLUMN rule_event_kind;
UPDATE github_task_binding SET rule_mode='automatic';
ALTER TABLE github_task_binding ADD COLUMN rule_event_kind TEXT NOT NULL DEFAULT ''
 CHECK(rule_event_kind IN ('','review_comment','review','check_run','issue_comment') AND (rule_mode='automatic' OR rule_event_kind!=''));
ALTER TABLE github_task_binding ADD COLUMN rule_check_conclusion TEXT NOT NULL DEFAULT ''
 CHECK(rule_check_conclusion='' OR (rule_event_kind='check_run' AND rule_check_conclusion='failure'));
UPDATE github_task_binding AS b SET (rule_mode,rule_event_kind,rule_check_conclusion) =
 (SELECT rule_mode,rule_event_kind,rule_check_conclusion FROM saved_github_rules s WHERE s.scope=b.scope);
DROP TABLE saved_github_rules;
CREATE TRIGGER github_task_binding_version BEFORE UPDATE ON github_task_binding WHEN
 NEW.version!=OLD.version+1 OR NEW.scope!=OLD.scope OR NEW.generation<OLD.generation
 BEGIN SELECT RAISE(ABORT,'invalid GitHub task binding'); END;

CREATE TABLE github_decision_v86 (
 observation_sequence INTEGER PRIMARY KEY REFERENCES github_observation(sequence),
 task_id TEXT NOT NULL REFERENCES task(task_id),
 mode TEXT NOT NULL CHECK(mode IN('observe','automatic')),
 event_kind TEXT NOT NULL CHECK(event_kind IN('review_comment','review','check_run','issue_comment')),
 matched INTEGER NOT NULL CHECK(matched IN(0,1)),
 expected_check_conclusion TEXT NOT NULL DEFAULT '' CHECK(expected_check_conclusion='' OR (event_kind='check_run' AND expected_check_conclusion='failure')),
 actual_check_conclusion TEXT NOT NULL DEFAULT '' CHECK(actual_check_conclusion IN('','action_required','cancelled','failure','neutral','success','skipped','stale','timed_out','startup_failure','unknown'))
) STRICT;
INSERT INTO github_decision_v86 SELECT observation_sequence,task_id,mode,event_kind,matched,expected_check_conclusion,actual_check_conclusion FROM github_rule_decision;
DROP TABLE github_rule_decision;
ALTER TABLE github_decision_v86 RENAME TO github_rule_decision;
CREATE TRIGGER github_rule_decision_immutable BEFORE UPDATE ON github_rule_decision
 BEGIN SELECT RAISE(ABORT,'immutable GitHub rule decision'); END;
CREATE TRIGGER github_rule_decision_retained BEFORE DELETE ON github_rule_decision
 BEGIN SELECT RAISE(ABORT,'GitHub rule decision retained'); END;
