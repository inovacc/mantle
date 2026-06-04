package bootstrap

import "github.com/inovacc/config"

// ConfigSource loads file+env configuration into app (populated in place). The
// default is viper-backed (inovacc/config); inject a fake in tests via
// WithConfigSource to avoid the loader's process-global state.
type ConfigSource interface {
	Load(app any, path, envPrefix string) error
}

// viperSource is the default, backed by github.com/inovacc/config v1.2.2.
type viperSource struct{}

func (viperSource) Load(app any, path, envPrefix string) error {
	if envPrefix != "" {
		config.SetEnvPrefix(envPrefix)
	}
	// InitServiceConfig stores app as the global Service and unmarshals
	// file+env into it in place (defaults already seeded by the caller).
	return config.InitServiceConfig(app, path)
}
