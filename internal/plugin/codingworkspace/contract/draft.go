package contract

const DraftContract = "steward.coding-draft-recipe/1"

type DraftFacts struct {
	Verified []bool `json:"verified"`
}
type DraftRecipe struct {
	Proof int    `json:"proof"`
	Body  string `json:"body"`
}
