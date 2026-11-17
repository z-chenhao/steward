-- Old immutable observations remain native. New whole-scan acceptance freezes
-- the selected source graph and exact continuation recipe without changing the
-- original event key/content, pending Main inputs or Tool/effect ledgers.
ALTER TABLE github_observation ADD COLUMN source_method_json TEXT
 CHECK(source_method_json IS NULL OR (json_valid(source_method_json)
 AND json_type(source_method_json,'$.graph_revision')='text'
 AND json_type(source_method_json,'$.recipe')='object'));
-- Read-only health uses the interval actually selected for a poll, without
-- executing current policy from status or mistaking failure retry for selection.
ALTER TABLE github_watch ADD COLUMN selected_interval_seconds INTEGER
 CHECK(selected_interval_seconds IS NULL OR selected_interval_seconds BETWEEN 5 AND 3600);
