-- Per-tree method may narrow, never replace, the frozen Host safety ceilings.
-- Historical/native trees do not acquire a policy retrospectively.
CREATE TABLE delegation_policy_binding (
 root_run_id TEXT PRIMARY KEY REFERENCES run_tree(root_run_id),
 graph_revision TEXT REFERENCES rsi_graph(revision),
 available INTEGER NOT NULL CHECK(available IN(0,1)),
 depth INTEGER NOT NULL CHECK(depth BETWEEN 0 AND 16),
 descendants INTEGER NOT NULL CHECK(descendants BETWEEN 0 AND 10000),
 instructions TEXT NOT NULL,
 CHECK((available=1 AND graph_revision IS NOT NULL) OR (available=0 AND graph_revision IS NULL AND depth=0 AND descendants=0))
) STRICT;
CREATE TRIGGER delegation_policy_immutable BEFORE UPDATE ON delegation_policy_binding BEGIN SELECT RAISE(ABORT,'immutable delegation policy'); END;
CREATE TRIGGER delegation_policy_retained BEFORE DELETE ON delegation_policy_binding BEGIN SELECT RAISE(ABORT,'retain delegation policy'); END;
CREATE TRIGGER delegation_policy_no_replace BEFORE INSERT ON delegation_policy_binding WHEN EXISTS(SELECT 1 FROM delegation_policy_binding WHERE root_run_id=NEW.root_run_id) BEGIN SELECT RAISE(ABORT,'delegation policy exists'); END;
