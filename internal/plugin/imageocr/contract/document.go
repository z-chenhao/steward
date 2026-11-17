package contract

type Word struct {
	Text       string  `json:"text"`
	X          int     `json:"x"`
	Y          int     `json:"y"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	Confidence float64 `json:"confidence"` // Engine score, 0–100; not a probability.
}
type Document struct {
	Method    string   `json:"method"`
	Languages []string `json:"languages"`
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	Text      string   `json:"text"`
	Words     []Word   `json:"words"`
	Truncated bool     `json:"truncated"`
}
