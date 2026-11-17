package sqlite

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUIManagedPreviewCannotCarryHostAuthorityOrExecutableFields(t *testing.T) {
	raw := `{"title":"Preview","components":[{"name":"generateSandboxedUi","props":{"initialHeight":240,"html":"<p>42</p>","css":"","cssComplete":true,"jsFunctions":"PRIVATE_CODE","jsExpressions":["PRIVATE_CODE()"]}},{"name":"steward.cards","props":{"items":[{"title":"Card","image_ref":"artifact:private"}]}}]}`
	out, err := inertUIPreview(raw)
	if err != nil || len(out.Components) != 2 {
		t.Fatal(out, err)
	}
	encoded, _ := json.Marshal(out)
	if strings.Contains(string(encoded), "PRIVATE_CODE") || strings.Contains(string(encoded), "artifact:private") {
		t.Fatal("unpublished preview executed or consumed private references", string(encoded))
	}
	for _, bad := range []string{
		`{"title":"x","components":[{"name":"steward.action","props":{}}]}`,
		`{"title":"x","components":[{"name":"Table","props":{},"message_id":"forged"}]}`,
		`{"title":"x","components":[{"name":"Table","props":{},"form_id":"forged"}]}`,
		`{"title":"x","components":[{"name":"Table","props":{},"approval_id":"forged"}]}`,
	} {
		if _, err := inertUIPreview(bad); err == nil {
			t.Fatal("untrusted preview gained Host authority", bad)
		}
	}
}
