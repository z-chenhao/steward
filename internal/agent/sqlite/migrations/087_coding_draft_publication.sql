CREATE TABLE coding_draft_publication (
 approval TEXT PRIMARY KEY CHECK(length(approval)=64),
 task_id TEXT NOT NULL REFERENCES task(task_id),
 plan_json TEXT NOT NULL CHECK(json_valid(plan_json)),
 status TEXT NOT NULL CHECK(status IN ('planned','started','unknown','published')),
 head TEXT NOT NULL DEFAULT '', number INTEGER NOT NULL DEFAULT 0, url TEXT NOT NULL DEFAULT '',
 CHECK((status='published' AND length(head)=40 AND number>0 AND url!='') OR (status!='published' AND number=0 AND url=''))
) STRICT;
CREATE TRIGGER coding_draft_plan_immutable BEFORE UPDATE ON coding_draft_publication
WHEN NEW.approval!=OLD.approval OR NEW.task_id!=OLD.task_id OR NEW.plan_json!=OLD.plan_json OR (OLD.head!='' AND NEW.head!=OLD.head)
 OR OLD.status='published' OR (NEW.status!=OLD.status AND NOT ((OLD.status='planned' AND NEW.status='started') OR (OLD.status='started' AND NEW.status IN ('unknown','published')) OR (OLD.status='unknown' AND NEW.status='published')))
BEGIN SELECT RAISE(ABORT,'invalid coding draft transition'); END;
