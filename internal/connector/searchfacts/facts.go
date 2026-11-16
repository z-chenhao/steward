// Package searchfacts describes existing V1 search-source facts, without
// ranking, preview limits or provider credentials. It is repository-private.
package searchfacts

type Row struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Content string `json:"content"`
}
type Result struct {
	Format string
	Text   string
	Rows   []Row
}
