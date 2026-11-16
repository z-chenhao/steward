-- Preserve accepted input independently from the producer's derived expression.
-- NULL names plans predating this input boundary; do not invent historical facts.
ALTER TABLE automation ADD COLUMN owner_frequency_json TEXT CHECK(owner_frequency_json IS NULL OR json_valid(owner_frequency_json));
CREATE TRIGGER automation_owner_frequency_version BEFORE UPDATE ON automation
 WHEN NEW.owner_frequency_json IS NOT OLD.owner_frequency_json
  AND NEW.schedule_version<=OLD.schedule_version
 BEGIN SELECT RAISE(ABORT,'frequency facts require a new schedule version'); END;
