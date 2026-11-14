package config

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
)

// Deployment overrides address the same typed policy and never read ambient env.
func (p *WorkflowPolicy) environmentField(key string) reflect.Value {
	v := reflect.ValueOf(p).Elem()
	for i := 0; i < v.NumField(); i++ {
		if key == "PROJECT_ONE_WORKFLOWS_"+strings.ToUpper(v.Type().Field(i).Tag.Get("yaml")) {
			return v.Field(i)
		}
	}
	return reflect.Value{}
}
func (p *WorkflowPolicy) applyEnvironment(key, raw string) error {
	v := p.environmentField(key)
	n, e := strconv.ParseInt(raw, 10, 64)
	if !v.IsValid() || e != nil || v.OverflowInt(n) {
		return errors.New("CONFIG_WORKFLOW_ENVIRONMENT_INVALID")
	}
	v.SetInt(n)
	return nil
}
func (c *WorkflowConfiguration) applyEnvironment(raw string) error {
	if raw != "true" && raw != "false" {
		return errors.New("CONFIG_WORKFLOW_ENVIRONMENT_INVALID")
	}
	c.Enabled = raw == "true"
	return nil
}
