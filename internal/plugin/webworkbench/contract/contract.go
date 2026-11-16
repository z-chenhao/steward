// Package contract is the private workbench presentation seam. It carries no page data.
package contract

const PluginID = "steward.web-workbench"
const Service = "steward.web-workbench/1"

type Asset struct {
	Source string `json:"source"`
	Digest string `json:"digest"`
}
type Translation struct {
	Key     string `json:"key"`
	English string `json:"en"`
	Chinese string `json:"zh_cn"`
}
type RegionSet struct {
	Kind  string   `json:"kind"`
	Slots []string `json:"slots"`
}
type HistoryLabel struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}
type Policy struct {
	Live           []RegionSet    `json:"live"`
	PageLive       []string       `json:"page_live"`
	PollMS         int            `json:"poll_ms"`
	MaxPollMS      int            `json:"max_poll_ms"`
	ReadTimeoutMS  int            `json:"read_timeout_ms"`
	ExecutionSheet bool           `json:"execution_sheet"`
	RowOpen        bool           `json:"row_open"`
	History        []HistoryLabel `json:"history"`
}
type Presentation struct {
	View       Asset  `json:"view"`
	Stylesheet Asset  `json:"stylesheet"`
	Copy       Asset  `json:"copy"`
	Policy     Policy `json:"policy"`
}
