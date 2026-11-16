package contract

// ConnectMarkupSource extends the same sealed resource for the current native
// and modal connection forms. It contains no account state or credentials.
const ConnectMarkupInterface = "steward.apps-view-markup/4"

type ConnectCopy struct {
	ConnectLabel        string `json:"connect_label"`
	AccountLabel        string `json:"account_label"`
	ConnectAccountLabel string `json:"connect_account_label"`
}

type ConnectViewSource struct {
	Program string      `json:"program"`
	English ConnectCopy `json:"en"`
	Chinese ConnectCopy `json:"zh-CN"`
}

type ConnectMarkupSource struct {
	AccountMarkupSource
	Connect ConnectViewSource `json:"connect"`
}
