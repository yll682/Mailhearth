package provider

import "fmt"

// Factory creates an adapter from persisted configuration. Core supplies
// decrypted management credentials.
type Factory func(apiBaseURL, username, apiKey string) Provider

var factories = map[ProviderKind]Factory{}

// Register installs one factory for a provider kind.
func Register(kind ProviderKind, factory Factory) {
	factories[kind] = factory
}

// Create returns the adapter for kind.
func Create(kind ProviderKind, apiBaseURL, username, apiKey string) (Provider, error) {
	f, ok := factories[kind]
	if !ok {
		return nil, fmt.Errorf("provider %q is not registered", kind)
	}
	return f(apiBaseURL, username, apiKey), nil
}
