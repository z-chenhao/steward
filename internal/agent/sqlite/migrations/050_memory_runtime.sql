-- Each execution database belongs to one trusted runtime principal. Existing
-- roots remain local; ownership cannot be reassigned by a request or restart.
CREATE TABLE runtime_owner(singleton INTEGER PRIMARY KEY CHECK(singleton=1),user_id TEXT NOT NULL CHECK(length(user_id) BETWEEN 1 AND 128)) STRICT;
INSERT INTO runtime_owner SELECT 1,'local' WHERE EXISTS(SELECT 1 FROM run) OR EXISTS(SELECT 1 FROM conversation_intent) OR EXISTS(SELECT 1 FROM auth_connection) OR EXISTS(SELECT 1 FROM model_profile) OR EXISTS(SELECT 1 FROM memory_mutation);
CREATE TRIGGER runtime_owner_immutable BEFORE UPDATE ON runtime_owner BEGIN SELECT RAISE(ABORT,'immutable runtime owner'); END;
CREATE TRIGGER runtime_owner_audit BEFORE DELETE ON runtime_owner BEGIN SELECT RAISE(ABORT,'retain runtime owner'); END;

-- Offered evidence, not a claim that the model used it or that it was useful.
CREATE TABLE memory_access(invocation_id TEXT NOT NULL REFERENCES tool_invocation(invocation_id),agent_id TEXT NOT NULL,source_ref TEXT NOT NULL,operation TEXT NOT NULL CHECK(operation IN('search','read')),created_at INTEGER NOT NULL,PRIMARY KEY(invocation_id,source_ref)) STRICT;
CREATE INDEX memory_access_ref ON memory_access(agent_id,source_ref,created_at);
CREATE TABLE memory_injection_access(run_id TEXT PRIMARY KEY REFERENCES run(run_id),memory_ref TEXT NOT NULL,created_at INTEGER NOT NULL) STRICT;
CREATE TABLE memory_dream_change(event_id TEXT NOT NULL REFERENCES memory_mutation(event_id),pass_index INTEGER NOT NULL,proposal_index INTEGER NOT NULL,memory_ref TEXT NOT NULL,layer TEXT NOT NULL,operation TEXT NOT NULL CHECK(operation IN('write','retire','unchanged')),PRIMARY KEY(event_id,pass_index,proposal_index)) STRICT;
CREATE TRIGGER memory_dream_change_immutable BEFORE UPDATE ON memory_dream_change BEGIN SELECT RAISE(ABORT,'immutable dream effect'); END;
CREATE TRIGGER memory_dream_change_audit BEFORE DELETE ON memory_dream_change BEGIN SELECT RAISE(ABORT,'retain dream effect'); END;

CREATE TRIGGER memory_access_immutable BEFORE UPDATE ON memory_access BEGIN SELECT RAISE(ABORT,'immutable memory access'); END;
CREATE TRIGGER memory_access_audit BEFORE DELETE ON memory_access BEGIN SELECT RAISE(ABORT,'retain memory access'); END;
CREATE TRIGGER memory_injection_access_immutable BEFORE UPDATE ON memory_injection_access BEGIN SELECT RAISE(ABORT,'immutable memory injection access'); END;
CREATE TRIGGER memory_injection_access_audit BEFORE DELETE ON memory_injection_access BEGIN SELECT RAISE(ABORT,'retain memory injection access'); END;
