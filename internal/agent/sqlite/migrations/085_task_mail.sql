CREATE TABLE task_mailbox (
 address TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES task(task_id), task_epoch INTEGER NOT NULL,
 privacy_epoch INTEGER NOT NULL, closed_at INTEGER, created_at INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX task_mailbox_open ON task_mailbox(task_id) WHERE closed_at IS NULL;
CREATE TRIGGER task_mailbox_close_only BEFORE UPDATE ON task_mailbox WHEN
 OLD.closed_at IS NOT NULL OR NEW.closed_at IS NULL OR NEW.address!=OLD.address OR NEW.task_id!=OLD.task_id OR
 NEW.task_epoch!=OLD.task_epoch OR NEW.privacy_epoch!=OLD.privacy_epoch OR NEW.created_at!=OLD.created_at
 BEGIN SELECT RAISE(ABORT,'mailbox close only'); END;
CREATE TABLE task_mail_receipt (
 address TEXT NOT NULL REFERENCES task_mailbox(address), message_key TEXT NOT NULL, content_hash TEXT NOT NULL,
 artifact_ref TEXT NOT NULL UNIQUE REFERENCES artifact(artifact_ref), method_graph TEXT NOT NULL REFERENCES rsi_graph(revision), received_at INTEGER NOT NULL,
 PRIMARY KEY(address,message_key)
) STRICT;
CREATE TRIGGER task_mail_receipt_immutable BEFORE UPDATE ON task_mail_receipt BEGIN SELECT RAISE(ABORT,'immutable mail receipt'); END;
CREATE TRIGGER task_mail_receipt_no_delete BEFORE DELETE ON task_mail_receipt BEGIN SELECT RAISE(ABORT,'retain mail receipt'); END;
CREATE TRIGGER task_mail_receipt_no_replace BEFORE INSERT ON task_mail_receipt WHEN EXISTS(
 SELECT 1 FROM task_mail_receipt WHERE (address=NEW.address AND message_key=NEW.message_key) OR artifact_ref=NEW.artifact_ref
) BEGIN SELECT RAISE(ABORT,'mail receipt exists'); END;
