package config

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

func yamlUnmarshal(b []byte, v any) error { return yaml.Unmarshal(b, v) }

// yamlUnmarshalStrict 严格模式：禁止未知 YAML 字段。
func yamlUnmarshalStrict(b []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	return dec.Decode(v)
}
