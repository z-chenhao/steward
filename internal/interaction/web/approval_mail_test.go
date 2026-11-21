package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/plugin/appsmethods/contract"
)

func mailApprovalSample() interaction.Approval {
	body := "各位好，附件是季度评审议程，请在周五前确认时间。会议将讨论交付进展、下一阶段预算和待决事项。" + strings.Repeat("如有补充，请提前回复，我们会把新议题加入议程。", 5) + "\n<script>fake()</script>完整正文末尾"
	params, _ := json.Marshal(map[string]any{"from_email": "alias@example.test", "recipient_email": "primary@example.test", "extra_recipients": []string{"other@example.test"}, "cc": []string{"copy@example.test"}, "bcc": []string{"archive@example.test"}, "subject": "季度方案", "attachment": map[string]any{"name": "议程.pdf", "s3key": "unique-upload"}, "body": body, "is_html": true})
	hash := strings.Repeat("c", 64)
	arguments, _ := json.Marshal(map[string]string{"tool_slug": "GMAIL_SEND_EMAIL", "version": "20261101_00", "descriptor_hash": hash, "arguments_json": string(params)})
	return interaction.Approval{ID: "mail-preview", Tool: "mcp__apps__b__bound__execute", Status: "pending", Hash: hash, Arguments: string(arguments), MailReview: &contract.FrozenMailReview{Account: "Gmail / work", Recipe: contract.MailRecipe{Toolkit: "gmail", Operation: "GMAIL_SEND_EMAIL", Fields: []contract.MailField{
		{Key: "from_email", Role: "from"}, {Key: "recipient_email", Role: "to"}, {Key: "extra_recipients", Role: "to"}, {Key: "cc", Role: "cc"}, {Key: "bcc", Role: "bcc"}, {Key: "subject", Role: "subject"}, {Key: "body", Role: "body"}, {Key: "attachment", Role: "attachments"},
	}}}}
}

func TestMailApprovalRendersFrozenEnvelopeAndCompleteValues(t *testing.T) {
	a := mailApprovalSample()
	fields := approvalFor(a).Mail
	want := map[string]string{"Authorized account": "Gmail / work", "Send-as address": "alias@example.test", "Email recipients": "primary@example.test\nother@example.test", "Email CC": "copy@example.test", "Email BCC": "archive@example.test", "Email subject": "季度方案", "is_html": "true"}
	for _, f := range fields {
		if value, ok := want[f.Name]; ok {
			if f.Value != value || f.Missing {
				t.Fatalf("%s = %q", f.Name, f.Value)
			}
			delete(want, f.Name)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing fields: %v", want)
	}
	s := languageServer()
	s.Core.(*updatesCore).approvals = []interaction.Approval{a}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, languageRequest("GET", "/", "zh-CN", ""))
	html := w.Body.String()
	for _, literal := range []string{"核对待发送的邮件", "议程.pdf", "完整正文末尾", "查看完整内容", `value="` + a.Hash + `"`, "archive@example.test", "unique-upload"} {
		if !strings.Contains(html, literal) {
			t.Errorf("missing %q", literal)
		}
	}
	if strings.Contains(html, "<script>fake()") {
		t.Error("email body interpreted as HTML")
	}
}

func TestMailApprovalKeepsOriginalViewForUnrecognizedOrAmbiguousCalls(t *testing.T) {
	a := mailApprovalSample()
	raw := a.Arguments
	var duplicate map[string]string
	if err := json.Unmarshal([]byte(raw), &duplicate); err != nil {
		t.Fatal(err)
	}
	duplicate["arguments_json"] = `{"bcc":"one","bcc":"two"}`
	duplicateCall, err := json.Marshal(duplicate)
	if err != nil {
		t.Fatal(err)
	}
	for _, other := range []string{strings.Replace(raw, "GMAIL_SEND_EMAIL", "GMAIL_SEND_DRAFT", 1),
		string(duplicateCall)} {
		a.Arguments = other
		v := approvalFor(a)
		if len(v.Mail) != 0 || v.Raw != other || v.Hash != a.Hash || len(v.Fields) == 0 {
			t.Fatal("lost exact review for unsupported operation")
		}
	}
	a.Arguments = raw
	a.MailReview = nil
	if len(approvalFor(a).Mail) != 0 {
		t.Fatal("historical invocation gained a current presentation")
	}
}
