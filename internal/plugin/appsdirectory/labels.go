package appsdirectory

import "github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"

func labels() *contract.LabelPolicy {
	return &contract.LabelPolicy{
		Named:   contract.LabelPlan{NameSource: "name", UpperInitial: true},
		Unnamed: contract.LabelPlan{NameSource: "slug", UpperInitial: true},
	}
}
