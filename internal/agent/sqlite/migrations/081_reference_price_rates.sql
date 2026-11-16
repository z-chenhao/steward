-- Observational prices are fixed with a new generation, never evaluated by a
-- producer in the usage writer. Existing generations retain their v1 estimator.
ALTER TABLE model_generation ADD COLUMN reference_rates_managed INTEGER NOT NULL DEFAULT 0 CHECK(reference_rates_managed IN (0,1));
CREATE TABLE generation_reference_rates (
 generation_id TEXT PRIMARY KEY REFERENCES model_generation(generation_id),
 graph_revision TEXT REFERENCES rsi_graph(revision),
 outcome TEXT NOT NULL CHECK(outcome IN ('known','unknown','unavailable')),
 rates_json TEXT NOT NULL CHECK(json_valid(rates_json)),
 CHECK((outcome='known')=(rates_json!='null'))
) STRICT;
CREATE TRIGGER generation_reference_rates_immutable BEFORE UPDATE ON generation_reference_rates
 BEGIN SELECT RAISE(ABORT,'immutable reference rates'); END;
CREATE TRIGGER generation_reference_rates_retained BEFORE DELETE ON generation_reference_rates
 BEGIN SELECT RAISE(ABORT,'retain reference rates'); END;
CREATE TRIGGER generation_reference_rates_no_replace BEFORE INSERT ON generation_reference_rates
 WHEN EXISTS(SELECT 1 FROM generation_reference_rates WHERE generation_id=NEW.generation_id)
 BEGIN SELECT RAISE(ABORT,'reference rates exist'); END;

CREATE TRIGGER model_generation_reference_rates_marker_immutable BEFORE UPDATE OF reference_rates_managed ON model_generation
 WHEN NEW.reference_rates_managed!=OLD.reference_rates_managed
 BEGIN SELECT RAISE(ABORT,'immutable reference rates marker'); END;
