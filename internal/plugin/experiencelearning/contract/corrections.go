package contract

const CorrectionRecipe = "steward.correction-recipe/1"

type CorrectionInput struct {
	Steps []string `json:"steps"`
}
type CorrectionPlan struct {
	IndependentRuns int    `json:"independent_runs"`
	Procedure       string `json:"procedure"`
}
