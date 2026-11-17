-- Per-execution migration boundaries are immutable facts, not current policy.
ALTER TABLE rsi_run ADD COLUMN managed_services_json TEXT NOT NULL DEFAULT '[]'
  CHECK(json_valid(managed_services_json) AND json_type(managed_services_json)='array');
DROP TRIGGER rsi_run_immutable_update;
-- Only the two pre-snapshot features have historical frozen policy opt-ins.
-- Do not retroactively opt old Runs into later feature implementations.
UPDATE rsi_run SET managed_services_json=(
  SELECT json_group_array(contract) FROM (
    SELECT 'steward.docx-text/1' AS contract WHERE EXISTS(
      SELECT 1 FROM run_context_policy p WHERE p.run_id=rsi_run.run_id AND json_extract(p.policy_json,'$.docx_plugin')=1)
    UNION ALL
    SELECT 'steward.image-ocr/1' WHERE EXISTS(
      SELECT 1 FROM run_context_policy p WHERE p.run_id=rsi_run.run_id AND json_extract(p.policy_json,'$.image_ocr_plugin')=1)
    ORDER BY contract
  )
);
CREATE TRIGGER rsi_run_immutable_update BEFORE UPDATE ON rsi_run BEGIN SELECT RAISE(ABORT,'immutable plugin binding'); END;
