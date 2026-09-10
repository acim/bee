package bee

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// configField is an active leaf. Parsing, validation, and help consume the same
// list, so none of them can accidentally apply a different command scope.
type configField struct {
	field  reflect.StructField
	value  reflect.Value
	prefix string
}

func (cl *commandLine) configFields(config any) ([]configField, error) {
	v := reflect.ValueOf(config)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return nil, ErrInvalidConfigType
	}
	var fields []configField
	if err := cl.collectFields(v.Elem(), "", "", nil, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

// collectFields checks every declaration before any values are parsed, including
// descendants of inactive structs. A nil scope means global configuration.
func (cl *commandLine) collectFields(v reflect.Value, prefix, path string, inherited []string, fields *[]configField) error {
	for i := range v.NumField() {
		field := v.Type().Field(i)
		value := v.Field(i)
		fieldPath := field.Name
		if path != "" {
			fieldPath = path + "." + field.Name
		}
		scope, err := cl.fieldScope(field, fieldPath, inherited)
		if err != nil {
			return err
		}
		if field.PkgPath != "" || !value.CanAddr() || !value.Addr().CanInterface() {
			return ErrInvalidConfigType
		}
		if field.Type.Kind() == reflect.Struct && field.Type != reflect.TypeFor[URL]() && field.Type != reflect.TypeFor[Time]() {
			if err := cl.collectFields(value, cl.newPrefix(field, prefix), fieldPath, scope, fields); err != nil {
				return err
			}
			continue
		}
		if scope == nil || slices.Contains(scope, cl.commandGroup) {
			*fields = append(*fields, configField{field: field, value: value, prefix: prefix})
		}
	}
	return nil
}

func (cl *commandLine) fieldScope(field reflect.StructField, path string, inherited []string) ([]string, error) {
	raw, present := field.Tag.Lookup("cmd")
	if !present {
		return inherited, nil
	}
	scope := strings.Split(raw, ",")
	for i, name := range scope {
		name = strings.TrimSpace(name)
		if name == "" || !slices.Contains(cl.commandGroups, name) {
			return nil, fmt.Errorf("%s cmd: invalid scope %q: %q must name a registered top-level command", path, raw, name)
		}
		if inherited != nil && !slices.Contains(inherited, name) {
			return nil, fmt.Errorf("%s cmd: invalid scope %q: command %q is outside parent scope %q", path, raw, name, strings.Join(inherited, ","))
		}
		scope[i] = name
	}
	return scope, nil
}
