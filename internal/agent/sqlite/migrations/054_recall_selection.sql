-- Host-owned authorized read snapshots, frozen before independent policy.
CREATE TABLE recall_candidate_snapshot (
 run_id TEXT NOT NULL REFERENCES run(run_id), operation_id TEXT NOT NULL,
 request_hash TEXT NOT NULL, privacy_epoch INTEGER NOT NULL,
 sealed INTEGER NOT NULL CHECK(sealed IN(0,1)), key_ref TEXT NOT NULL,
 payload TEXT,
 created_at INTEGER NOT NULL,
 CHECK((sealed=0 AND key_ref='') OR (sealed=1 AND length(key_ref)>0)), PRIMARY KEY(run_id,operation_id)
) STRICT;
CREATE TRIGGER recall_candidate_identity BEFORE UPDATE ON recall_candidate_snapshot
 WHEN NEW.run_id!=OLD.run_id OR NEW.operation_id!=OLD.operation_id
 OR NEW.request_hash!=OLD.request_hash OR NEW.privacy_epoch!=OLD.privacy_epoch
 OR NEW.key_ref!=OLD.key_ref OR NEW.sealed!=OLD.sealed OR NEW.created_at!=OLD.created_at
 OR NEW.payload IS NOT NULL OR OLD.payload IS NULL
 BEGIN SELECT RAISE(ABORT,'immutable recall candidate snapshot'); END;
CREATE TRIGGER recall_candidate_no_delete BEFORE DELETE ON recall_candidate_snapshot
 BEGIN SELECT RAISE(ABORT,'retained recall candidate identity'); END;
CREATE TRIGGER recall_candidate_no_replace BEFORE INSERT ON recall_candidate_snapshot
 WHEN EXISTS(SELECT 1 FROM recall_candidate_snapshot WHERE run_id=NEW.run_id AND operation_id=NEW.operation_id)
 BEGIN SELECT RAISE(ABORT,'recall candidate identity exists'); END;
