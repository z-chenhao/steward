package environment

import (
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/computer/projectcontainer"
)

func TestCodingContainerConfigurationDoesNotRebindNativeWorkspaceScope(t *testing.T) {
	before := Config{Root: "/private/execution", RepositorySource: "/private/source", ToolchainRoots: []string{"/private/tools"}}
	hash := ResourceHash(before)
	after := before
	after.ProjectContainer = &projectcontainer.Config{Socket: "/private/docker.sock", Image: "sha256:" + strings.Repeat("a", 64)}
	if after.Validate() != nil || ResourceHash(after) != hash || ResourceHash(&after) != hash {
		t.Fatal("container setup changed native Task/Run scope")
	}
	first := after.ProjectContainer.ResourceHash(hash)
	after.ProjectContainer.Image = "sha256:" + strings.Repeat("b", 64)
	if after.ProjectContainer.ResourceHash(hash) == first || ResourceHash(after) != hash {
		t.Fatal("prepared image identity did not vary independently")
	}
	after.ProjectContainer.Image = "latest"
	if after.Validate() == nil {
		t.Fatal("mutable image tag admitted")
	}
}
