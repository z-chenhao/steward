-- Keep the owning version's plain display text available during producer stop.
ALTER TABLE automation ADD COLUMN presentation_json TEXT CHECK(presentation_json IS NULL OR json_valid(presentation_json));
CREATE TRIGGER automation_presentation_version BEFORE UPDATE ON automation
 WHEN NEW.presentation_json IS NOT OLD.presentation_json
  AND NEW.schedule_version<=OLD.schedule_version
 BEGIN SELECT RAISE(ABORT,'presentation requires a new schedule version'); END;
