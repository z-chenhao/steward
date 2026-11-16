package appsinterface

import "github.com/z-chenhao/assistant/pkg/rsiplugin"

type viewSource struct {
	Program []string `json:"program"`
}

func readOwnViewSource(name string) ([]byte, viewSource, error) {
	var source viewSource
	raw, err := readOwnViewBytes(name)
	if err != nil {
		return nil, source, err
	}
	err = rsiplugin.Decode(raw, &source)
	return raw, source, err
}
