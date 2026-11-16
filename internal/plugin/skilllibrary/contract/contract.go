// Package contract exposes private discovery metadata and resource entry policy.
// Skill files, fingerprints and access authority stay inside Host.
package contract

const PluginID = "steward.skill-library"
const SearchContract = "steward.skill-discovery/1"
const ResourceContract = "steward.skill-entry/1"

type Entry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
type SearchFacts struct {
	Query   string  `json:"query"`
	Limit   int     `json:"limit"`
	Entries []Entry `json:"entries"`
}
type SearchPlan struct {
	Indices []int `json:"indices"`
}
type ResourceFacts struct {
	Path string `json:"path"`
}
type ResourcePlan struct {
	Path string `json:"path"`
}
