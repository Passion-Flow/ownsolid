package config

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

const maximumKnowledgeGatewayInventoryBytes int64 = 1 << 20

// Both input forms address the existing typed owner; ambient environment is ignored.
func (p *KnowledgePolicy) environmentField(key string) reflect.Value {
	v := reflect.ValueOf(p).Elem()
	for i := 0; i < v.NumField(); i++ {
		if key == "PROJECT_ONE_KNOWLEDGE_"+strings.ToUpper(v.Type().Field(i).Tag.Get("yaml")) {
			return v.Field(i)
		}
	}
	return reflect.Value{}
}
func (p *KnowledgePolicy) applyEnvironment(key, raw string) error {
	bad := errors.New("CONFIG_KNOWLEDGE_ENVIRONMENT_INVALID")
	v := p.environmentField(key)
	if !v.IsValid() {
		return bad
	}
	if v.Kind() == reflect.Float64 {
		n, e := strconv.ParseFloat(raw, 64)
		if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return bad
		}
		v.SetFloat(n)
		return nil
	}
	n, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || v.OverflowInt(n) {
		return bad
	}
	v.SetInt(n)
	return nil
}
func knowledgeConfigurationEnvironment(key string) bool {
	switch key {
	case "PROJECT_ONE_KNOWLEDGE_ENABLED", "PROJECT_ONE_KNOWLEDGE_PYTHON_EXECUTABLE", "PROJECT_ONE_KNOWLEDGE_WORKER_FILE", "PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY", "PROJECT_ONE_KNOWLEDGE_SCRATCH_ROOT", "PROJECT_ONE_KNOWLEDGE_GATEWAYS_FILE":
		return true
	}
	return false
}
func (c *KnowledgeConfiguration) applyEnvironment(key, value string) error {
	bad := errors.New("CONFIG_KNOWLEDGE_ENVIRONMENT_INVALID")
	switch key {
	case "PROJECT_ONE_KNOWLEDGE_ENABLED":
		if value != "true" && value != "false" {
			return bad
		}
		c.Enabled = value == "true"
	case "PROJECT_ONE_KNOWLEDGE_GATEWAYS_FILE":
		raw, e := readPrivateFile(value, maximumKnowledgeGatewayInventoryBytes, "CONFIG_KNOWLEDGE_GATEWAYS_PRIVATE_FILE_INVALID")
		if e != nil {
			return e
		}
		var node yaml.Node
		// JSON-only reference with exact field case, duplicates/null/aliases rejected.
		if !json.Valid(raw) || yaml.Unmarshal(raw, &node) != nil || len(node.Content) != 1 || strictKnowledgeYAML(node.Content[0], reflect.TypeOf([]KnowledgeGateway{})) != nil {
			return bad
		}
		var gateways []KnowledgeGateway
		if node.Content[0].Decode(&gateways) != nil || gateways == nil {
			return bad
		}
		c.Gateways = gateways
	default:
		if !knowledgePath(value) {
			return bad
		}
		switch key {
		case "PROJECT_ONE_KNOWLEDGE_PYTHON_EXECUTABLE":
			c.PythonExecutable = value
		case "PROJECT_ONE_KNOWLEDGE_WORKER_FILE":
			c.WorkerFile = value
		case "PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY":
			c.ModelDirectory = value
		case "PROJECT_ONE_KNOWLEDGE_SCRATCH_ROOT":
			c.ScratchRoot = value
		default:
			return bad
		}
	}
	return nil
}
