package sqlite

// Actual consumers: retained pre-recipe Run service lists and schema69's
// migration-only legacy owner evidence, plus explicitly native SDK fixtures.
// Once the feature activates, no owner failure/deactivation uses this path.
// Delete when those real historical executions/installations are retired.
func (s *Store) legacyCreation() creationFact { return creationFact{days: s.Policy.RetentionDays} }
