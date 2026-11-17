-- Feedback outlives its creating Run. Preserve the accepted response recipe,
-- independently of later plugin activation, retirement or response channels.
CREATE TABLE task_feedback_policy_binding (
 feedback_ref TEXT PRIMARY KEY REFERENCES task_feedback(feedback_ref),
 graph_revision TEXT REFERENCES rsi_graph(revision),
 response_guidance TEXT NOT NULL
) STRICT;
INSERT INTO task_feedback_policy_binding SELECT feedback_ref,NULL,'If continuing through task_manage append, copy feedback_ref to feedbackRef, pass artifact_refs as artifactRefs, and use the exact Task reference;' FROM task_feedback;
CREATE TRIGGER task_feedback_policy_immutable BEFORE UPDATE ON task_feedback_policy_binding BEGIN SELECT RAISE(ABORT,'immutable feedback policy'); END;
CREATE TRIGGER task_feedback_policy_retained BEFORE DELETE ON task_feedback_policy_binding BEGIN SELECT RAISE(ABORT,'retain feedback policy'); END;
CREATE TRIGGER task_feedback_policy_no_replace BEFORE INSERT ON task_feedback_policy_binding WHEN EXISTS(SELECT 1 FROM task_feedback_policy_binding WHERE feedback_ref=NEW.feedback_ref) BEGIN SELECT RAISE(ABORT,'feedback policy exists'); END;
