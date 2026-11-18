package sqlite

// Newer fixture layers must be removed by every older reconstructed schema.
const removeTaskMailFixtureSchema = `DROP TABLE IF EXISTS task_mail_receipt; DROP TABLE IF EXISTS task_mailbox; `

const removeFeedbackScopeFixtureSchema = removeTaskMailFixtureSchema + `DROP TRIGGER task_feedback_scope_immutable;
DROP TRIGGER task_feedback_no_replace;
DROP INDEX task_feedback_pending;
ALTER TABLE task_feedback DROP COLUMN separate_kinds;
CREATE UNIQUE INDEX task_feedback_pending ON task_feedback(task_id) WHERE status='pending';
CREATE TRIGGER task_feedback_no_replace BEFORE INSERT ON task_feedback WHEN
 EXISTS(SELECT 1 FROM task_feedback WHERE feedback_ref=NEW.feedback_ref OR
 (task_id=NEW.task_id AND status='pending' AND NEW.status='pending'))
 BEGIN SELECT RAISE(ABORT,'task feedback identity exists'); END; `

const removeCodingProjectFixtureSchema = removeFeedbackScopeFixtureSchema + `DROP TABLE IF EXISTS coding_project_resource; DROP TABLE IF EXISTS coding_project_plan; DROP TABLE IF EXISTS coding_task_environment; `

const removeCodingCheckFixtureSchema = removeCodingProjectFixtureSchema + `DROP TABLE IF EXISTS coding_check_plan; `

const removeReferenceRatesFixtureSchema = removeCodingCheckFixtureSchema + `DROP TABLE IF EXISTS generation_reference_rates; DROP TRIGGER IF EXISTS model_generation_reference_rates_marker_immutable; ALTER TABLE model_generation DROP COLUMN reference_rates_managed; `

const removeArtifactRecipeFixtureSchema = removeReferenceRatesFixtureSchema + `DROP TRIGGER IF EXISTS automation_task_label_immutable; ALTER TABLE automation_fire DROP COLUMN task_title; ALTER TABLE automation_fire DROP COLUMN task_label_graph; ALTER TABLE automation_fire DROP COLUMN task_label_managed; DROP TRIGGER IF EXISTS automation_presentation_version; ALTER TABLE automation DROP COLUMN presentation_json; DROP TRIGGER IF EXISTS automation_owner_local_version; ALTER TABLE automation DROP COLUMN owner_local_json; DROP TRIGGER IF EXISTS automation_owner_frequency_version; ALTER TABLE automation DROP COLUMN owner_frequency_json; DROP TRIGGER IF EXISTS automation_fire_method_immutable; ALTER TABLE automation_fire DROP COLUMN method_graph; DROP TRIGGER IF EXISTS automation_method_graph_version; ALTER TABLE automation DROP COLUMN method_graph; DROP TABLE IF EXISTS ui_form_view_snapshot; DROP TABLE IF EXISTS ui_chart_view_snapshot; DROP TABLE IF EXISTS ui_table_view_snapshot; DROP TABLE IF EXISTS ui_cards_view_snapshot; DROP TABLE IF EXISTS ui_plan_view_snapshot; DROP TABLE IF EXISTS ui_form_answer; DROP TRIGGER IF EXISTS artifact_semantic_immutable; ALTER TABLE artifact DROP COLUMN semantic_kind; ALTER TABLE artifact DROP COLUMN method_graph; DROP TABLE IF EXISTS legacy_artifact_owner; DROP TABLE IF EXISTS run_artifact_recipe; DROP TABLE IF EXISTS run_artifact_request; `

const removeGitHubSourceMethodFixtureSchema = removeArtifactRecipeFixtureSchema + `ALTER TABLE github_observation DROP COLUMN source_method_json; ALTER TABLE github_watch DROP COLUMN selected_interval_seconds; `

const removeCodeSearchFixtureSchema = removeGitHubSourceMethodFixtureSchema + `DROP TABLE IF EXISTS code_search_reply; `

const removeComputerActionFixtureSchema = removeCodeSearchFixtureSchema + `DROP TABLE IF EXISTS computer_action_plan; `

const removeRSIFixtureSchema = removeComputerActionFixtureSchema + `DROP TABLE IF EXISTS task_label_plan; DROP TABLE IF EXISTS code_report_method_binding; DROP TABLE IF EXISTS code_import_policy_binding; DROP TABLE IF EXISTS shell_method_plan; DROP TABLE IF EXISTS file_method_plan; DROP TABLE IF EXISTS code_description_reply; DROP TABLE IF EXISTS delegation_policy_binding; DROP TABLE IF EXISTS task_feedback_policy_binding; DROP TABLE IF EXISTS experience_policy_binding; DROP TABLE IF EXISTS memory_policy_binding; DROP TABLE IF EXISTS recall_candidate_snapshot; DROP TABLE IF EXISTS rsi_call; DROP TABLE IF EXISTS rsi_event; DROP TABLE IF EXISTS rsi_state; DROP TABLE IF EXISTS rsi_run; DROP TABLE IF EXISTS rsi_activation; DROP TABLE IF EXISTS rsi_decision; DROP TABLE IF EXISTS rsi_candidate; DROP TABLE IF EXISTS rsi_policy; DROP TABLE IF EXISTS rsi_control; DROP TABLE IF EXISTS rsi_quarantine; DROP TABLE IF EXISTS rsi_retired; DROP TABLE IF EXISTS rsi_graph; DROP TABLE IF EXISTS rsi_package; DROP TABLE IF EXISTS rsi_audit; `

// All model_generation columns in the original schema7-80 protocol. New
// schema81 metadata is checked separately; original bytes remain compared.
const historicalGenerationColumnsV80 = `generation_id,run_id,step_index,request_json,request_hash,owner_token,status,result_json,error_code,attempt_count,created_at,completed_at`
