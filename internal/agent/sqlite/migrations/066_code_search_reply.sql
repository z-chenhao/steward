-- A selected search reply can change subsequent script branching. Retain the
-- complete accepted reply before acknowledgment, bound to its original VM.
CREATE TABLE code_search_reply (
 execution_id TEXT NOT NULL REFERENCES code_execution(execution_id),
 request_hash TEXT NOT NULL,
 reply_json TEXT NOT NULL CHECK(json_valid(reply_json)),
 PRIMARY KEY(execution_id,request_hash)
) STRICT;
CREATE TRIGGER code_search_immutable BEFORE UPDATE ON code_search_reply BEGIN SELECT RAISE(ABORT,'immutable code search'); END;
CREATE TRIGGER code_search_retained BEFORE DELETE ON code_search_reply BEGIN SELECT RAISE(ABORT,'retain code search'); END;
CREATE TRIGGER code_search_no_replace BEFORE INSERT ON code_search_reply WHEN EXISTS(SELECT 1 FROM code_search_reply WHERE execution_id=NEW.execution_id AND request_hash=NEW.request_hash) BEGIN SELECT RAISE(ABORT,'code search exists'); END;
