package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/prompt"
)

func TestConversationMethodsPreserveHostSafetyIdentityAndSources(t *testing.T) {
	in := InitialContext(ContextOptions{Position: "main"})
	in = append(in, TextMessage("user", "authorized original input"))
	baseline := MainMethods{Role: strings.TrimPrefix(prompt.Main(), MainIdentity), Execution: strings.TrimSuffix(prompt.MainExecution(), "\n</rules>")}
	out, ok := ApplyMainMethods(in, baseline)
	if !ok || !reflect.DeepEqual(in, out) {
		t.Fatal("default policy changed original bytes", ok)
	}
	changed, ok := ApplyMainMethods(in, MainMethods{Role: " Different communication method.", Execution: "- Different bounded work method."})
	if !ok {
		t.Fatal("independent policy rejected")
	}
	if changed[0].SystemSections[1] != in[0].SystemSections[1] || !strings.HasPrefix(messageText(changed[0]), MainIdentity) || !reflect.DeepEqual(changed[1:], in[1:]) || in[0].SystemSections[0].Text != prompt.Main() {
		t.Fatal("policy changed protected facts or source")
	}
	if _, valid := FrozenSystemSections(changed); !valid {
		t.Fatal("typed frozen system no longer validates")
	}
	for _, position := range []string{"root", "child"} {
		if _, ok := ApplyMainMethods(InitialContext(ContextOptions{Position: position, Task: "private Worker input"}), baseline); ok {
			t.Fatal("Main policy invaded Worker", position)
		}
	}
	for _, bad := range []MainMethods{{Role: "x"}, {Role: strings.Repeat("x", 16385), Execution: "y"}, {Role: "x", Execution: string([]byte{0xff})}} {
		if _, ok := ApplyMainMethods(in, bad); ok {
			t.Fatal("unbounded/invalid methods accepted")
		}
	}
}
