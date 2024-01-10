// Code generated by mdatagen. DO NOT EDIT.

package metadata

import "go.opentelemetry.io/collector/confmap"

// ResourceAttributeConfig provides common config for a particular resource attribute.
type ResourceAttributeConfig struct {
	Enabled bool `mapstructure:"enabled"`

	enabledSetByUser bool
}

func (rac *ResourceAttributeConfig) Unmarshal(parser *confmap.Conf) error {
	if parser == nil {
		return nil
	}
	err := parser.Unmarshal(rac)
	if err != nil {
		return err
	}
	rac.enabledSetByUser = parser.IsSet("enabled")
	return nil
}

// ResourceAttributesConfig provides config for resourcedetectionprocessor/docker resource attributes.
type ResourceAttributesConfig struct {
	HostName ResourceAttributeConfig `mapstructure:"host.name"`
	OsType   ResourceAttributeConfig `mapstructure:"os.type"`
}

func DefaultResourceAttributesConfig() ResourceAttributesConfig {
	return ResourceAttributesConfig{
		HostName: ResourceAttributeConfig{
			Enabled: true,
		},
		OsType: ResourceAttributeConfig{
			Enabled: true,
		},
	}
}
