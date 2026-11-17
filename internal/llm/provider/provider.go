package provider

import (
	_ "embed"
	"encoding/json"
	"net/url"
	"strings"
)

//go:embed provider_keys.json
var providerKeyData []byte

func APIKeyEnv(provider string) []string {
	var keys map[string][]string
	_ = json.Unmarshal(providerKeyData, &keys)
	return append([]string(nil), keys[provider]...)
}

func (o Options) Resolve(m Model) Options {
	get := func(names ...string) string {
		for _, name := range names {
			if value := o.Env[name]; value != "" {
				return value
			}
		}
		return ""
	}
	if o.APIKey == "" && o.AccessToken == "" {
		keys := APIKeyEnv(m.Provider)
		if len(keys) == 0 {
			keys = []string{strings.ToUpper(strings.ReplaceAll(m.Provider, "-", "_")) + "_API_KEY"}
		}
		o.APIKey = get(keys...)
	}
	if o.AccessToken == "" && m.API == BedrockConverse {
		o.AccessToken = get("AWS_BEARER_TOKEN_BEDROCK")
	}
	if m.API == AnthropicMessages && o.AccessToken == "" && (get("ANTHROPIC_AUTH_TOKEN") == o.APIKey && o.APIKey != "" || strings.Contains(o.APIKey, "sk-ant-oat")) {
		o.AccessToken, o.APIKey = o.APIKey, ""
	}
	if o.AWS.AccessKey == "" {
		o.AWS.AccessKey = get("AWS_ACCESS_KEY_ID")
		o.AWS.SecretKey = get("AWS_SECRET_ACCESS_KEY")
		o.AWS.SessionToken = get("AWS_SESSION_TOKEN")
	}
	if o.AWS.Region == "" {
		o.AWS.Region = get("AWS_REGION", "AWS_DEFAULT_REGION")
	}
	if o.Project == "" {
		o.Project = get("GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT")
	}
	if o.Location == "" {
		o.Location = get("GOOGLE_CLOUD_LOCATION")
	}
	if o.AccessToken == "" && m.API == GoogleVertex {
		o.AccessToken = get("GOOGLE_CLOUD_ACCESS_TOKEN")
	}
	if o.APIVersion == "" {
		o.APIVersion = get("AZURE_OPENAI_API_VERSION")
	}
	if o.Deployment == "" {
		o.Deployment = get("AZURE_OPENAI_DEPLOYMENT_NAME")
	}
	return o
}

func resolveModel(m Model, o Options) Model {
	for _, name := range []string{"CLOUDFLARE_ACCOUNT_ID", "CLOUDFLARE_GATEWAY_ID"} {
		if value := o.Env[name]; value != "" {
			m.BaseURL = strings.ReplaceAll(m.BaseURL, "{"+name+"}", url.PathEscape(value))
		}
	}
	if m.API == BedrockConverse && o.AWS.Region != "" {
		endpoint, _ := url.Parse(m.BaseURL)
		standard := endpoint != nil && endpoint.Scheme == "https" && strings.HasPrefix(endpoint.Hostname(), "bedrock-runtime.") && (strings.HasSuffix(endpoint.Hostname(), ".amazonaws.com") || strings.HasSuffix(endpoint.Hostname(), ".amazonaws.com.cn"))
		if m.BaseURL == "" || standard {
			suffix := ".amazonaws.com"
			if strings.HasPrefix(o.AWS.Region, "cn-") {
				suffix += ".cn"
			}
			m.BaseURL = "https://bedrock-runtime." + o.AWS.Region + suffix
		}
	}
	if m.API == GoogleVertex && m.BaseURL == "" {
		m.BaseURL = "https://aiplatform.googleapis.com/v1"
	}
	if m.API == AzureResponses && m.BaseURL == "" && o.Env["AZURE_OPENAI_ENDPOINT"] != "" {
		base := strings.TrimRight(o.Env["AZURE_OPENAI_ENDPOINT"], "/")
		if !strings.HasSuffix(base, "/openai/v1") {
			base += "/openai/v1"
		}
		m.BaseURL = base
	}
	return m
}

// FetchDeferred and CancelDeferred implement durable OpenAI Responses handles.
// Other wire protocols can expose asynchronous state via their native endpoint.
type DeferredHandle struct {
	API API    `json:"api"`
	ID  string `json:"id"`
	URL string `json:"url,omitempty"`
}

func cloneMap(in map[string]any) map[string]any {
	raw, _ := json.Marshal(in)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}
