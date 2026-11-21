package appsmethods

import (
	"context"

	"github.com/z-chenhao/assistant/internal/plugin/appsmethods/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func mailReview(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var input struct{}
	if err := rsiplugin.Decode(call.Input, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, contract.MailRecipe{
		Toolkit: "gmail", Operation: "GMAIL_SEND_EMAIL",
		Fields: []contract.MailField{
			{Key: "from_email", Role: "from"}, {Key: "recipient_email", Role: "to"}, {Key: "to", Role: "to"},
			{Key: "extra_recipients", Role: "to"}, {Key: "cc", Role: "cc"}, {Key: "bcc", Role: "bcc"},
			{Key: "subject", Role: "subject"}, {Key: "attachment", Role: "attachments"}, {Key: "body", Role: "body"},
		},
	})
}
