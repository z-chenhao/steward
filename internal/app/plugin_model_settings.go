package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"strings"
	"unicode/utf8"
)

func (a *App) readModelSettings(ctx context.Context, facts contract.SettingsFacts) (contract.SettingsProjection, bool, error) {
	if a.prepareFeaturePlugins != nil {
		if err := a.prepareFeaturePlugins(ctx); err != nil {
			return contract.SettingsProjection{}, true, err
		}
	}
	return a.readModelSettingsWithHost(ctx, a.Plugins(), facts)
}
func (a *App) readModelSettingsWithHost(ctx context.Context, host rsi.Host, facts contract.SettingsFacts) (contract.SettingsProjection, bool, error) {
	revision, managed, err := a.store.ModelSettingsSelection(ctx)
	if err != nil || !managed {
		return contract.SettingsProjection{}, managed, err
	}
	if revision == "" {
		return contract.SettingsProjection{}, true, rsi.ErrNotFound
	}
	input, err := json.Marshal(facts)
	if err != nil {
		return contract.SettingsProjection{}, true, err
	}
	raw, err := host.ReadService(ctx, revision, contract.Settings, input, nil)
	if err != nil {
		return contract.SettingsProjection{}, true, err
	}
	var out contract.SettingsProjection
	if err = rsiplugin.DecodeResult(raw, &out); err != nil {
		return contract.SettingsProjection{}, true, err
	}
	if facts.WithView {
		binding, e := host.ServiceInstance(ctx, revision, contract.Settings)
		if e != nil {
			return contract.SettingsProjection{}, true, e
		}
		pkg, e := host.Catalog.Load(binding.PackageDigest)
		if e != nil {
			return contract.SettingsProjection{}, true, e
		}
		for _, asset := range []struct {
			name, file string
			value      *contract.View
		}{{contract.ViewResource, contract.ViewFile, out.View}, {contract.CopyResource, contract.CopyFile, out.Copy}} {
			count := 0
			digest := ""
			for _, resource := range pkg.Manifest.Resources {
				if resource.Name == asset.name && resource.Kind == "view" && resource.Path == asset.file {
					count++
					for _, file := range pkg.Manifest.Files {
						if file.Path == resource.Path {
							digest = file.Digest
						}
					}
				}
			}
			v := asset.value
			if count != 1 || digest == "" || v == nil || v.Resource != asset.name || v.Digest != digest || len(v.Source) == 0 || len(v.Source) > 65536 || !utf8.ValidString(v.Source) || rsiplugin.FileDigest([]byte(v.Source)) != digest {
				return contract.SettingsProjection{}, true, rsi.ErrInvalid
			}
		}
		var copy []contract.Translation
		if rsiplugin.Decode([]byte(out.Copy.Source), &copy) != nil || len(copy) > 512 {
			return contract.SettingsProjection{}, true, rsi.ErrInvalid
		}
		seen := map[string]bool{}
		for _, entry := range copy {
			if strings.TrimSpace(entry.Key) == "" || entry.Value == "" || len(entry.Key) > 4096 || len(entry.Value) > 4096 || seen[entry.Key] || strings.ContainsRune(entry.Key, 0) || strings.ContainsRune(entry.Value, 0) {
				return contract.SettingsProjection{}, true, rsi.ErrInvalid
			}
			seen[entry.Key] = true
		}
		if e = host.Store.CheckGraph(ctx, revision); e != nil {
			return contract.SettingsProjection{}, true, e
		}
	} else if out.View != nil || out.Copy != nil {
		return contract.SettingsProjection{}, true, rsi.ErrInvalid
	}
	return out, true, nil
}
