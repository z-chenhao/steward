-- Earlier unbundled carriers froze context without an rsi_run row. Preserve
-- those accepted executions as explicit native work; never attach today's
-- selection or rewrite their policies, requests, receipts or checkpoints.
INSERT INTO rsi_run(run_id,graph_revision,managed_services_json)
 SELECT r.run_id,NULL,'[]' FROM run r
 WHERE NOT EXISTS(SELECT 1 FROM rsi_run b WHERE b.run_id=r.run_id);
