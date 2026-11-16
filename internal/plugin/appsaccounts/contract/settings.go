package contract

import (
	"encoding/binary"
	"errors"
)

const Settings = "steward.apps-settings/1"
const SettingsInputID = "settings-apps"
const SettingsPageSize = 16384

type SettingsRequest struct {
	Mode   string `json:"mode"`
	Count  int    `json:"count"`
	Offset int    `json:"offset"`
	Remove int    `json:"remove"`
}
type SettingsPlan struct {
	Indices []int `json:"indices"`
}

func EncodeSettingsApps(apps []string) []byte {
	var raw []byte
	for _, app := range apps {
		raw = binary.LittleEndian.AppendUint64(raw, uint64(len(app)))
		raw = append(raw, app...)
	}
	return raw
}
func DecodeSettingsApps(raw []byte, count int) ([]string, error) {
	if count < 0 || count > len(raw)/8 {
		return nil, errors.New("invalid settings count")
	}
	apps := make([]string, count)
	for i := range apps {
		if len(raw) < 8 {
			return nil, errors.New("truncated settings app")
		}
		size := binary.LittleEndian.Uint64(raw)
		raw = raw[8:]
		if size > uint64(len(raw)) {
			return nil, errors.New("invalid settings app size")
		}
		apps[i] = string(raw[:int(size)])
		raw = raw[int(size):]
	}
	if len(raw) != 0 {
		return nil, errors.New("trailing settings facts")
	}
	return apps, nil
}
