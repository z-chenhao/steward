-- A form policy result is an accepted input fact, not a pending plugin call.
-- Written atomically with main_input; failed admission leaves no frozen answer.
CREATE TABLE ui_form_answer (
 form_id TEXT PRIMARY KEY CHECK(length(form_id)=64),
 source_message_id TEXT NOT NULL REFERENCES conversation_intent(message_id),
 source_run_id TEXT NOT NULL REFERENCES run(run_id),
 graph_revision TEXT NOT NULL REFERENCES rsi_graph(revision),
 input_hash TEXT NOT NULL CHECK(length(input_hash)=71 AND substr(input_hash,1,7)='sha256:'),
 accepted_message_id TEXT NOT NULL UNIQUE REFERENCES main_input(message_id),
 result_json TEXT NOT NULL CHECK(json_valid(result_json))
) STRICT;
CREATE TRIGGER ui_form_answer_immutable BEFORE UPDATE ON ui_form_answer
 BEGIN SELECT RAISE(ABORT,'immutable accepted form answer'); END;
CREATE TRIGGER ui_form_answer_retained BEFORE DELETE ON ui_form_answer
 BEGIN SELECT RAISE(ABORT,'retain accepted form answer'); END;
CREATE TRIGGER ui_form_answer_no_replace BEFORE INSERT ON ui_form_answer
 WHEN EXISTS(SELECT 1 FROM ui_form_answer WHERE form_id=NEW.form_id)
 BEGIN SELECT RAISE(ABORT,'form answer exists'); END;
