-- Documentation can affect script branching. Freeze the complete accepted
-- reply before VM acknowledgment, separately from invocation-local JSON store.
CREATE TABLE code_description_reply (
 execution_id TEXT NOT NULL REFERENCES code_execution(execution_id),
 request_hash TEXT NOT NULL,
 reply_json TEXT NOT NULL CHECK(json_valid(reply_json)),
 PRIMARY KEY(execution_id,request_hash)
) STRICT;
CREATE TRIGGER code_description_immutable BEFORE UPDATE ON code_description_reply BEGIN SELECT RAISE(ABORT,'immutable code description'); END;
CREATE TRIGGER code_description_retained BEFORE DELETE ON code_description_reply BEGIN SELECT RAISE(ABORT,'retain code description'); END;
CREATE TRIGGER code_description_no_replace BEFORE INSERT ON code_description_reply WHEN EXISTS(SELECT 1 FROM code_description_reply WHERE execution_id=NEW.execution_id AND request_hash=NEW.request_hash) BEGIN SELECT RAISE(ABORT,'code description exists'); END;
