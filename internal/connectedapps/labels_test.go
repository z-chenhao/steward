package connectedapps

import (
	"errors"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

func TestEditorialLabelsProjectOriginalFieldsWithPresencePlans(t *testing.T) {
	selection := DirectorySelection{mode: directoryManaged, Catalogue: contract.Catalogue{Labels: &contract.LabelPolicy{Named: contract.LabelPlan{NameSource: "slug"}, Unnamed: contract.LabelPlan{NameSource: "slug", UpperInitial: true}}}}
	result, err := selection.Label("private original name", "private_slug")
	if err != nil || result.Name != "private_slug" || result.Initial != "p" {
		t.Fatal("original field/case reference changed", result, err)
	}
	result, err = selection.Label("", "汉字")
	if err != nil || result.Name != "汉字" || result.Initial != "汉" {
		t.Fatal("missing-name presence plan changed", result, err)
	}
}
func TestEditorialLabelsNeverInferNativeFromInvalidSelection(t *testing.T) {
	for _, selection := range []DirectorySelection{{}, {mode: directoryManaged}, {mode: directoryManaged, Catalogue: contract.Catalogue{Labels: &contract.LabelPolicy{Named: contract.LabelPlan{NameSource: "account"}, Unnamed: contract.LabelPlan{NameSource: "slug"}}}}} {
		if value, err := selection.Label("private name", "private_slug"); !errors.Is(err, ErrUnavailable) || value != (CardLabel{}) {
			t.Fatal("invalid selection invented a name/default", value, err)
		}
	}
}
