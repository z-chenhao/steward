-- The accepted editor minute authenticates an unchanged local stop without
-- asking a producer or discarding the saved instant's precision and offset.
ALTER TABLE automation ADD COLUMN owner_local_json TEXT CHECK(owner_local_json IS NULL OR json_valid(owner_local_json));
CREATE TRIGGER automation_owner_local_version BEFORE UPDATE ON automation
 WHEN NEW.owner_local_json IS NOT OLD.owner_local_json
  AND NEW.schedule_version<=OLD.schedule_version
 BEGIN SELECT RAISE(ABORT,'local editor facts require a new schedule version'); END;
