CREATE TABLE rsi_package (
  digest TEXT PRIMARY KEY, plugin_id TEXT NOT NULL, version TEXT NOT NULL,
  manifest_json TEXT NOT NULL CHECK(json_valid(manifest_json)),
  UNIQUE(plugin_id,version)
);
CREATE TABLE rsi_graph (
  revision TEXT PRIMARY KEY, graph_json TEXT NOT NULL CHECK(json_valid(graph_json)),
  order_json TEXT NOT NULL CHECK(json_valid(order_json))
);
CREATE TABLE rsi_control (
  singleton INTEGER PRIMARY KEY CHECK(singleton=1), active_graph TEXT REFERENCES rsi_graph(revision),
  epoch INTEGER NOT NULL, stopped INTEGER NOT NULL CHECK(stopped IN (0,1))
);
INSERT INTO rsi_control VALUES(1,NULL,0,0);
CREATE TABLE rsi_run (
  run_id TEXT PRIMARY KEY REFERENCES run(run_id), graph_revision TEXT REFERENCES rsi_graph(revision)
);
INSERT INTO rsi_run SELECT run_id,NULL FROM run;
CREATE TABLE rsi_policy (policy_id TEXT PRIMARY KEY, policy_json TEXT NOT NULL CHECK(json_valid(policy_json)));
CREATE TABLE rsi_candidate (
  candidate_id TEXT PRIMARY KEY, candidate_json TEXT NOT NULL CHECK(json_valid(candidate_json)),
  target TEXT NOT NULL REFERENCES rsi_graph(revision), policy_id TEXT NOT NULL REFERENCES rsi_policy(policy_id)
);
CREATE TABLE rsi_decision (
  decision_id TEXT PRIMARY KEY, candidate_id TEXT NOT NULL REFERENCES rsi_candidate(candidate_id),
  target TEXT NOT NULL REFERENCES rsi_graph(revision), passed INTEGER NOT NULL CHECK(passed IN (0,1)),
  evidence_json TEXT NOT NULL CHECK(json_valid(evidence_json))
);
CREATE TABLE rsi_activation (
  operation_id TEXT PRIMARY KEY, request_hash TEXT NOT NULL, receipt_json TEXT NOT NULL CHECK(json_valid(receipt_json))
);
CREATE TABLE rsi_quarantine (revision TEXT PRIMARY KEY REFERENCES rsi_graph(revision));
CREATE TABLE rsi_retired (digest TEXT PRIMARY KEY REFERENCES rsi_package(digest));
CREATE TABLE rsi_audit (
  sequence INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, reference TEXT NOT NULL,
  detail_json TEXT NOT NULL CHECK(json_valid(detail_json))
);
CREATE TABLE rsi_state (
  namespace TEXT PRIMARY KEY, plugin_id TEXT NOT NULL, format_version INTEGER NOT NULL,
  schema_hash TEXT NOT NULL, version INTEGER NOT NULL, value_json TEXT NOT NULL CHECK(json_valid(value_json))
);
CREATE TABLE rsi_call (
  run_id TEXT NOT NULL REFERENCES rsi_run(run_id), instance_id TEXT NOT NULL, operation_id TEXT NOT NULL,
  request_hash TEXT NOT NULL, state_version INTEGER NOT NULL,
  result_json TEXT CHECK(result_json IS NULL OR json_valid(result_json)),
  PRIMARY KEY(run_id,instance_id,operation_id)
);
CREATE TABLE rsi_event (
  event_id TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES rsi_run(run_id),
  stage TEXT NOT NULL, step INTEGER NOT NULL, reference TEXT NOT NULL,
  source_status TEXT NOT NULL, reason TEXT NOT NULL,
  input_json TEXT CHECK(input_json IS NULL OR json_valid(input_json)),
  status TEXT NOT NULL CHECK(status IN('pending','running','completed','failed')),
  token TEXT, lease_until INTEGER NOT NULL DEFAULT 0, error_code TEXT NOT NULL DEFAULT ''
);
CREATE INDEX rsi_event_pending ON rsi_event(status,lease_until);
CREATE TRIGGER rsi_run_immutable_update BEFORE UPDATE ON rsi_run BEGIN SELECT RAISE(ABORT,'immutable plugin binding'); END;
CREATE TRIGGER rsi_run_immutable_delete BEFORE DELETE ON rsi_run BEGIN SELECT RAISE(ABORT,'immutable plugin binding'); END;
CREATE TRIGGER rsi_package_immutable_update BEFORE UPDATE ON rsi_package BEGIN SELECT RAISE(ABORT,'immutable plugin package'); END;
CREATE TRIGGER rsi_package_immutable_delete BEFORE DELETE ON rsi_package BEGIN SELECT RAISE(ABORT,'retained plugin package'); END;
CREATE TRIGGER rsi_graph_immutable_update BEFORE UPDATE ON rsi_graph BEGIN SELECT RAISE(ABORT,'immutable plugin graph'); END;
CREATE TRIGGER rsi_graph_immutable_delete BEFORE DELETE ON rsi_graph BEGIN SELECT RAISE(ABORT,'retained plugin graph'); END;
CREATE TRIGGER rsi_policy_immutable_update BEFORE UPDATE ON rsi_policy BEGIN SELECT RAISE(ABORT,'immutable plugin policy'); END;
CREATE TRIGGER rsi_policy_immutable_delete BEFORE DELETE ON rsi_policy BEGIN SELECT RAISE(ABORT,'retained plugin policy'); END;
CREATE TRIGGER rsi_candidate_immutable_update BEFORE UPDATE ON rsi_candidate BEGIN SELECT RAISE(ABORT,'immutable plugin candidate'); END;
CREATE TRIGGER rsi_decision_immutable_update BEFORE UPDATE ON rsi_decision BEGIN SELECT RAISE(ABORT,'immutable plugin decision'); END;
CREATE TRIGGER rsi_audit_immutable_update BEFORE UPDATE ON rsi_audit BEGIN SELECT RAISE(ABORT,'immutable plugin audit'); END;
CREATE TRIGGER rsi_audit_immutable_delete BEFORE DELETE ON rsi_audit BEGIN SELECT RAISE(ABORT,'immutable plugin audit'); END;
