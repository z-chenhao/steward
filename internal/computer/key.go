package computer

import "context"

// KeyDispatch is local authority from Host, never a candidate callback or RPC.
type KeyDispatch struct {
	Recipe      KeyRecipe                   `json:"-"`
	BeforeInput func(context.Context) error `json:"-"`
}
