-- Candidate policy and original proposal are immutable, independently of Run.
-- Existing candidates preserve their original method and verification recipe.
CREATE TABLE experience_policy_binding (
 candidate_id TEXT PRIMARY KEY REFERENCES skill_candidate(candidate_id),
 managed INTEGER NOT NULL CHECK(managed IN(0,1)),
 graph_revision TEXT REFERENCES rsi_graph(revision),
 required_runs INTEGER NOT NULL CHECK(required_runs BETWEEN 1 AND 128),
 introduction TEXT NOT NULL,
 original_procedure TEXT NOT NULL,
 CHECK((managed=0 AND graph_revision IS NULL) OR (managed=1 AND graph_revision IS NOT NULL))
) STRICT;
INSERT INTO experience_policy_binding SELECT candidate_id,0,NULL,CASE WHEN reason='success' THEN 2 ELSE 1 END,'',procedure FROM skill_candidate;
CREATE TRIGGER experience_policy_immutable BEFORE UPDATE ON experience_policy_binding BEGIN SELECT RAISE(ABORT,'immutable experience policy'); END;
CREATE TRIGGER experience_policy_retained BEFORE DELETE ON experience_policy_binding BEGIN SELECT RAISE(ABORT,'retain experience policy'); END;
CREATE TRIGGER experience_policy_no_replace BEFORE INSERT ON experience_policy_binding WHEN EXISTS(SELECT 1 FROM experience_policy_binding WHERE candidate_id=NEW.candidate_id) BEGIN SELECT RAISE(ABORT,'experience policy exists'); END;
