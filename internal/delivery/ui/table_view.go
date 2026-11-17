package ui

// Internal Host view data, not a plugin SDK or an arbitrary DOM capability.
// Worker code has no DOM/parent; only the fixed driver applies bounded output.
type TableDescription struct {
	Title     string  `json:"title"`
	Sort      *string `json:"sort"`
	Direction int     `json:"direction"`
}
type TableSortContext struct {
	Sort        int              `json:"sort"`
	Direction   int              `json:"direction"`
	Description TableDescription `json:"description"`
}
type TableView struct {
	HTML            string
	CSS             string
	Worker          string
	Styles          []byte
	CompactMaxWidth int
	Contexts        []TableSortContext
}

// TableExport contains authorized original facts, never candidate file bytes.
type TableExport struct {
	Title   string     `json:"title"`
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}
