package bootstrap

type options struct {
	source     ConfigSource
	envPrefix  string
	configPath string
	version    string
	appName    string
}

// Option customizes Configure.
type Option func(*options)

// WithConfigSource overrides the default viper-backed config loader (e.g. a fake in tests).
func WithConfigSource(s ConfigSource) Option {
	return func(o *options) {
		if s != nil {
			o.source = s
		}
	}
}

// WithEnvPrefix sets the environment-variable prefix (e.g. "APP" → APP_SERVICE_LOGGER_LEVEL).
func WithEnvPrefix(prefix string) Option { return func(o *options) { o.envPrefix = prefix } }

// WithConfigPath sets the config file path used when --config is not provided.
func WithConfigPath(path string) Option { return func(o *options) { o.configPath = path } }

// WithVersion sets the version reported by --version.
func WithVersion(v string) Option { return func(o *options) { o.version = v } }

// WithAppName sets the service name (OTel resource, version string). Defaults to the root command name.
func WithAppName(name string) Option { return func(o *options) { o.appName = name } }
