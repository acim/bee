package bee

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/iancoleman/strcase"
)

var durationType = reflect.TypeOf(time.Duration(0))

// Errors.
var (
	ErrInvalidConfigType = errors.New("invalid config type")
	ErrUnsupportedType   = errors.New("type not supported")
)

type requiredField struct {
	fieldName string
	flagName  string
	envName   string
}

type commandLine struct {
	flagSet       *flag.FlagSet
	output        io.Writer
	lookupEnvFunc func(string) (string, bool)
	name          string
	errorHandling flag.ErrorHandling
	help          bool
	required      []requiredField
}

func newCommandLine(name string) *commandLine {
	a := &commandLine{ //nolint:exhaustruct
		flagSet:       flag.NewFlagSet(name, flag.ContinueOnError),
		output:        os.Stderr,
		lookupEnvFunc: os.LookupEnv,
		name:          name,
		errorHandling: flag.ExitOnError,
	}

	a.flagSet.SetOutput(a.output)

	return a
}

func (cl *commandLine) parse(config any, flags []string) error {
	cl.required = nil
	cl.help = false

	if err := cl.subParse(config, flags, ""); err != nil {
		return cl.exit(err)
	}

	if err := cl.flagSet.Parse(flags); err != nil {
		return cl.exit(err)
	}

	if err := cl.validateRequired(); err != nil {
		return cl.exit(err)
	}

	if err := cl.validate(config); err != nil {
		return cl.exit(err)
	}

	return nil
}

func (cl *commandLine) subParse(config any, flags []string, prefix string) error { //nolint:cyclop
	cl.parseHelp(flags)

	v := reflect.ValueOf(config)

	if !v.IsValid() {
		return ErrInvalidConfigType
	}

	if v.Kind() != reflect.Pointer || v.IsNil() {
		return ErrInvalidConfigType
	}

	t := v.Type()

	if t.Elem().Kind() != reflect.Struct {
		return ErrInvalidConfigType
	}

	v = v.Elem()

	for i := 0; i < v.NumField(); i++ {
		field := t.Elem().Field(i)

		flagName := cl.flagName(field, prefix)

		envVarName := cl.envVarName(field, prefix)

		usage := cl.usage(field, envVarName, prefix)

		fieldValue := v.Field(i)
		if field.PkgPath != "" || !fieldValue.CanAddr() || !fieldValue.Addr().CanInterface() {
			return ErrInvalidConfigType
		}

		p := fieldValue.Addr().Interface()

		// Recurse if got struct which is not of URL type
		_, oku := p.(*URL)
		_, okt := p.(*Time)

		if field.Type.Kind() == reflect.Struct && !oku && !okt {
			if err := cl.subParse(p, flags, cl.newPrefix(field, prefix)); err != nil {
				return err
			}

			continue
		}

		if err := cl.parseRequired(field, flagName, envVarName); err != nil {
			return err
		}

		envVarValue, ok := cl.lookupEnvFunc(envVarName)
		if ok && !cl.help {
			if err := cl.validateFlagName(flagName); err != nil {
				return fmt.Errorf("%s env: %w", field.Name, err)
			}

			if err := cl.parseValue(field.Type.Kind(), p, flagName, envVarValue, usage); err != nil {
				return fmt.Errorf("%s env: %w", field.Name, err)
			}

			continue
		}

		if err := cl.validateFlagName(flagName); err != nil {
			return fmt.Errorf("%s def: %w", field.Name, err)
		}

		if err := cl.parseValue(field.Type.Kind(), p, flagName, field.Tag.Get("def"), usage); err != nil {
			return fmt.Errorf("%s def: %w", field.Name, err)
		}
	}

	return nil
}

func (cl *commandLine) parseRequired(field reflect.StructField, flagName string, envName string) error {
	if _, ok := field.Tag.Lookup("req"); !ok {
		return nil
	}

	if _, ok := field.Tag.Lookup("def"); ok {
		return fmt.Errorf("%s req: cannot combine req and def tags", field.Name)
	}

	if _, ok := cl.lookupEnvFunc(envName); ok && !cl.help {
		return nil
	}

	cl.required = append(cl.required, requiredField{
		fieldName: field.Name,
		flagName:  flagName,
		envName:   envName,
	})

	return nil
}

func (cl *commandLine) validateRequired() error {
	if cl.help {
		return nil
	}

	setFlags := map[string]struct{}{}
	cl.flagSet.Visit(func(f *flag.Flag) {
		setFlags[f.Name] = struct{}{}
	})

	for _, field := range cl.required {
		if _, ok := setFlags[field.flagName]; ok {
			continue
		}

		return fmt.Errorf(
			"%s req: required value missing; set %s or -%s",
			field.fieldName,
			field.envName,
			field.flagName,
		)
	}

	return nil
}

func (cl *commandLine) validate(config any) error {
	if cl.help {
		return nil
	}

	v := reflect.ValueOf(config)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return ErrInvalidConfigType
	}

	return cl.validateStruct(v.Elem())
}

func (cl *commandLine) validateStruct(v reflect.Value) error {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		value := v.Field(i)
		if field.PkgPath != "" {
			return ErrInvalidConfigType
		}

		if value.Kind() == reflect.Struct && !isSpecialStructValue(value) {
			if err := cl.validateStruct(value); err != nil {
				return err
			}

			continue
		}

		if err := cl.validateField(field, value); err != nil {
			return err
		}
	}

	return nil
}

func isSpecialStructValue(v reflect.Value) bool {
	if !v.CanAddr() {
		return false
	}

	switch v.Addr().Interface().(type) {
	case *URL, *Time:
		return true
	default:
		return false
	}
}

func (cl *commandLine) validateField(field reflect.StructField, value reflect.Value) error {
	if err := validateMinMax(field, value); err != nil {
		return err
	}

	if err := validateOneOf(field, value); err != nil {
		return err
	}

	if err := validateLengths(field, value); err != nil {
		return err
	}

	if err := validateRegex(field, value); err != nil {
		return err
	}

	if err := validatePrefixSuffix(field, value); err != nil {
		return err
	}

	if err := validateNonzero(field, value); err != nil {
		return err
	}

	return nil
}

func validateMinMax(field reflect.StructField, value reflect.Value) error {
	if min := field.Tag.Get("min"); min != "" {
		if err := compareMinimum(field, value, min); err != nil {
			return err
		}
	}

	if max := field.Tag.Get("max"); max != "" {
		if err := compareMaximum(field, value, max); err != nil {
			return err
		}
	}

	return nil
}

func compareMinimum(field reflect.StructField, value reflect.Value, min string) error { //nolint:cyclop
	if value.Type() == durationType {
		limit, err := time.ParseDuration(min)
		if err != nil {
			return fmt.Errorf("%s min: parsing duration %q: %w", field.Name, min, err)
		}

		got := value.Interface().(time.Duration)
		if got < limit {
			return fmt.Errorf("%s min: value %s must be >= %s", field.Name, got, limit)
		}

		return nil
	}

	switch value.Kind() { //nolint:exhaustive
	case reflect.Int, reflect.Int64:
		limit, err := strconv.ParseInt(min, 10, 64) //nolint:gomnd
		if err != nil {
			return fmt.Errorf("%s min: parsing int %q: %w", field.Name, min, err)
		}

		got := value.Int()
		if got < limit {
			return fmt.Errorf("%s min: value %d must be >= %d", field.Name, got, limit)
		}
	case reflect.Uint, reflect.Uint64:
		limit, err := strconv.ParseUint(min, 10, 64) //nolint:gomnd
		if err != nil {
			return fmt.Errorf("%s min: parsing uint %q: %w", field.Name, min, err)
		}

		got := value.Uint()
		if got < limit {
			return fmt.Errorf("%s min: value %d must be >= %d", field.Name, got, limit)
		}
	case reflect.Float64:
		limit, err := strconv.ParseFloat(min, 64) //nolint:gomnd
		if err != nil {
			return fmt.Errorf("%s min: parsing float %q: %w", field.Name, min, err)
		}

		got := value.Float()
		if math.IsNaN(got) {
			return fmt.Errorf("%s min: value NaN must be a number", field.Name)
		}

		if got < limit {
			return fmt.Errorf("%s min: value %g must be >= %g", field.Name, got, limit)
		}
	default:
		return fmt.Errorf("%s min: unsupported type %s", field.Name, value.Type())
	}

	return nil
}

func compareMaximum(field reflect.StructField, value reflect.Value, max string) error { //nolint:cyclop
	if value.Type() == durationType {
		limit, err := time.ParseDuration(max)
		if err != nil {
			return fmt.Errorf("%s max: parsing duration %q: %w", field.Name, max, err)
		}

		got := value.Interface().(time.Duration)
		if got > limit {
			return fmt.Errorf("%s max: value %s must be <= %s", field.Name, got, limit)
		}

		return nil
	}

	switch value.Kind() { //nolint:exhaustive
	case reflect.Int, reflect.Int64:
		limit, err := strconv.ParseInt(max, 10, 64) //nolint:gomnd
		if err != nil {
			return fmt.Errorf("%s max: parsing int %q: %w", field.Name, max, err)
		}

		got := value.Int()
		if got > limit {
			return fmt.Errorf("%s max: value %d must be <= %d", field.Name, got, limit)
		}
	case reflect.Uint, reflect.Uint64:
		limit, err := strconv.ParseUint(max, 10, 64) //nolint:gomnd
		if err != nil {
			return fmt.Errorf("%s max: parsing uint %q: %w", field.Name, max, err)
		}

		got := value.Uint()
		if got > limit {
			return fmt.Errorf("%s max: value %d must be <= %d", field.Name, got, limit)
		}
	case reflect.Float64:
		limit, err := strconv.ParseFloat(max, 64) //nolint:gomnd
		if err != nil {
			return fmt.Errorf("%s max: parsing float %q: %w", field.Name, max, err)
		}

		got := value.Float()
		if math.IsNaN(got) {
			return fmt.Errorf("%s max: value NaN must be a number", field.Name)
		}

		if got > limit {
			return fmt.Errorf("%s max: value %g must be <= %g", field.Name, got, limit)
		}
	default:
		return fmt.Errorf("%s max: unsupported type %s", field.Name, value.Type())
	}

	return nil
}

func validateOneOf(field reflect.StructField, value reflect.Value) error {
	tag, ok := field.Tag.Lookup("oneof")
	if !ok {
		return nil
	}

	allowed, err := requireTagList(field, "oneof", tag)
	if err != nil {
		return err
	}

	got := validationString(value)
	for _, option := range allowed {
		if got == option {
			return nil
		}
	}

	return fmt.Errorf("%s oneof: value %q must be one of %s", field.Name, got, strings.Join(allowed, ", "))
}

func validateLengths(field reflect.StructField, value reflect.Value) error {
	for _, tag := range []string{"len", "minlen", "maxlen"} {
		limit, ok, err := validationLength(field, tag)
		if err != nil {
			return err
		}

		if !ok {
			continue
		}

		got, ok := lengthValue(value)
		if !ok {
			return fmt.Errorf("%s %s: unsupported type %s", field.Name, tag, value.Type())
		}

		switch tag {
		case "len":
			if got != limit {
				return fmt.Errorf("%s len: length %d must equal %d", field.Name, got, limit)
			}
		case "minlen":
			if got < limit {
				return fmt.Errorf("%s minlen: length %d must be >= %d", field.Name, got, limit)
			}
		case "maxlen":
			if got > limit {
				return fmt.Errorf("%s maxlen: length %d must be <= %d", field.Name, got, limit)
			}
		}
	}

	return nil
}

func validationLength(field reflect.StructField, tag string) (int, bool, error) {
	raw, ok := field.Tag.Lookup(tag)
	if !ok {
		return 0, false, nil
	}

	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0, true, fmt.Errorf("%s %s: parsing length %q: %w", field.Name, tag, raw, err)
	}

	return limit, true, nil
}

func lengthValue(value reflect.Value) (int, bool) {
	switch value.Kind() { //nolint:exhaustive
	case reflect.String:
		return value.Len(), true
	case reflect.Slice:
		switch value.Interface().(type) {
		case StringSlice, IntSlice:
			return value.Len(), true
		default:
			return 0, false
		}
	default:
		return 0, false
	}
}

func validateRegex(field reflect.StructField, value reflect.Value) error {
	pattern, ok := field.Tag.Lookup("regex")
	if !ok {
		return nil
	}

	if value.Kind() != reflect.String {
		return fmt.Errorf("%s regex: unsupported type %s", field.Name, value.Type())
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("%s regex: compiling %q: %w", field.Name, pattern, err)
	}

	got := value.String()
	if !re.MatchString(got) {
		return fmt.Errorf("%s regex: value %q must match %s", field.Name, got, pattern)
	}

	return nil
}

func validatePrefixSuffix(field reflect.StructField, value reflect.Value) error {
	got, ok := stringLikeValue(value)

	if tag, present := field.Tag.Lookup("prefix"); present {
		if !ok {
			return fmt.Errorf("%s prefix: unsupported type %s", field.Name, value.Type())
		}

		prefixes, err := requireTagList(field, "prefix", tag)
		if err != nil {
			return err
		}

		if !hasAnyPrefix(got, prefixes) {
			return fmt.Errorf("%s prefix: value %q must start with one of %s", field.Name, got, strings.Join(prefixes, ", "))
		}
	}

	if tag, present := field.Tag.Lookup("suffix"); present {
		if !ok {
			return fmt.Errorf("%s suffix: unsupported type %s", field.Name, value.Type())
		}

		suffixes, err := requireTagList(field, "suffix", tag)
		if err != nil {
			return err
		}

		if !hasAnySuffix(got, suffixes) {
			return fmt.Errorf("%s suffix: value %q must end with one of %s", field.Name, got, strings.Join(suffixes, ", "))
		}
	}

	return nil
}

func stringLikeValue(value reflect.Value) (string, bool) {
	if value.Kind() == reflect.String {
		return value.String(), true
	}

	if value.CanAddr() {
		if v, ok := value.Addr().Interface().(*URL); ok {
			return v.String(), true
		}
	}

	return "", false
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}

	return false
}

func hasAnySuffix(value string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}

	return false
}

func validateNonzero(field reflect.StructField, value reflect.Value) error {
	if _, ok := field.Tag.Lookup("nonzero"); !ok {
		return nil
	}

	if value.CanAddr() {
		switch v := value.Addr().Interface().(type) {
		case *URL:
			if v == nil || v.URL == nil || v.String() == "" {
				return fmt.Errorf("%s nonzero: value must not be zero", field.Name)
			}

			return nil
		case *Time:
			if v == nil || v.Time == nil || v.IsZero() {
				return fmt.Errorf("%s nonzero: value must not be zero", field.Name)
			}

			return nil
		}
	}

	if value.IsZero() {
		return fmt.Errorf("%s nonzero: value must not be zero", field.Name)
	}

	return nil
}

func requireTagList(field reflect.StructField, tagName string, raw string) ([]string, error) {
	values := splitTagList(raw)
	if len(values) == 0 {
		return nil, fmt.Errorf("%s %s: empty value list", field.Name, tagName)
	}

	return values, nil
}

func splitTagList(tag string) []string {
	parts := strings.Split(tag, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}

	return values
}

func validationString(value reflect.Value) string {
	if value.Type() == durationType {
		return value.Interface().(time.Duration).String()
	}

	if value.CanAddr() {
		switch v := value.Addr().Interface().(type) {
		case *URL:
			return v.String()
		case *Time:
			return v.String()
		}
	}

	switch value.Kind() { //nolint:exhaustive
	case reflect.String:
		return value.String()
	case reflect.Bool:
		return strconv.FormatBool(value.Bool())
	case reflect.Int, reflect.Int64:
		return strconv.FormatInt(value.Int(), 10)
	case reflect.Uint, reflect.Uint64:
		return strconv.FormatUint(value.Uint(), 10)
	case reflect.Float64:
		return strconv.FormatFloat(value.Float(), 'g', -1, 64) //nolint:gomnd
	default:
		return fmt.Sprint(value.Interface())
	}
}

func (cl *commandLine) validateFlagName(flagName string) error {
	if cl.flagSet.Lookup(flagName) != nil {
		return fmt.Errorf("duplicate flag %q: %w", flagName, ErrInvalidConfigType)
	}

	return nil
}

func (*commandLine) flagName(sf reflect.StructField, prefix string) string {
	if f := sf.Tag.Get("flag"); f != "" {
		return f
	}

	n := sf.Name

	if prefix != "" {
		n = fmt.Sprintf("%s-%s", prefix, n)
	}

	return strcase.ToKebab(n)
}

func (cl *commandLine) envVarName(sf reflect.StructField, prefix string) string {
	if e := sf.Tag.Get("env"); e != "" {
		return e
	}

	n := fmt.Sprintf("%s_%s", cl.name, sf.Name)
	if prefix != "" {
		n = fmt.Sprintf("%s_%s_%s", cl.name, prefix, sf.Name)
	}

	return strcase.ToScreamingSnake(n)
}

func (*commandLine) usage(sf reflect.StructField, env string, prefix string) string {
	if u := sf.Tag.Get("help"); u != "" {
		return fmt.Sprintf("%s (env %s)", u, env)
	}

	n := sf.Name
	if prefix != "" {
		n = fmt.Sprintf("%s %s", prefix, sf.Name)
	}

	return fmt.Sprintf("%s (env %s)", strcase.ToDelimited(n, ' '), env)
}

func (cl *commandLine) parseHelp(flags []string) {
	if len(flags) == 0 {
		return
	}

	for _, f := range flags {
		if f == "--help" || f == "-help" || f == "--h" || f == "-h" {
			cl.help = true

			return
		}
	}
}

func (*commandLine) newPrefix(sf reflect.StructField, prefix string) string {
	if prefix != "" {
		return fmt.Sprintf("%s-%s", prefix, sf.Name)
	}

	return sf.Name
}

func (cl *commandLine) parseValue(kind reflect.Kind, varPointer any, flag, value, usage string) error { //nolint:cyclop
	switch kind { //nolint:exhaustive
	case reflect.Bool:
		return cl.parseBool(varPointer.(*bool), flag, value, usage) //nolint:forcetypeassert
	case reflect.String:
		cl.flagSet.StringVar(varPointer.(*string), flag, value, usage) //nolint:forcetypeassert

		return nil
	case reflect.Uint:
		return cl.parseUint(varPointer.(*uint), flag, value, usage) //nolint:forcetypeassert
	case reflect.Uint64:
		return cl.parseUint64(varPointer.(*uint64), flag, value, usage) //nolint:forcetypeassert
	case reflect.Int:
		return cl.parseInt(varPointer.(*int), flag, value, usage) //nolint:forcetypeassert
	case reflect.Int64:
		switch varPointer := varPointer.(type) {
		case *time.Duration:
			return cl.parseDuration(varPointer, flag, value, usage)
		case *int64:
			return cl.parseInt64(varPointer, flag, value, usage)
		}
	case reflect.Float64:
		return cl.parseFloat64(varPointer.(*float64), flag, value, usage) //nolint:forcetypeassert
	case reflect.Struct:
		switch varPointer := varPointer.(type) {
		case *URL:
			return cl.parseURL(varPointer, flag, value, usage)
		case *Time:
			return cl.parseTime(varPointer, flag, value, usage)
		}
	case reflect.Slice:
		switch varPointer := varPointer.(type) {
		case *StringSlice:
			return cl.parseStringSlice(varPointer, flag, value, usage)
		case *IntSlice:
			return cl.parseIntSlice(varPointer, flag, value, usage)
		}
	}

	return fmt.Errorf("parsing value: %w: %v", ErrUnsupportedType, kind)
}

func (cl *commandLine) parseBool(p *bool, flag, value, usage string) error {
	if value == "" {
		cl.flagSet.BoolVar(p, flag, false, usage)

		return nil
	}

	val, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("parsing bool %q: %w", value, err)
	}

	cl.flagSet.BoolVar(p, flag, val, usage)

	return nil
}

func (cl *commandLine) parseUint(p *uint, flag, value, usage string) error {
	if value == "" {
		cl.flagSet.UintVar(p, flag, 0, usage)

		return nil
	}

	val, err := strconv.ParseUint(value, 10, 32) //nolint:gomnd
	if err != nil {
		return fmt.Errorf("parsing uint %q: %w", value, err)
	}

	cl.flagSet.UintVar(p, flag, uint(val), usage)

	return nil
}

func (cl *commandLine) parseUint64(p *uint64, flag, value, usage string) error {
	if value == "" {
		cl.flagSet.Uint64Var(p, flag, 0, usage)

		return nil
	}

	val, err := strconv.ParseUint(value, 10, 64) //nolint:gomnd
	if err != nil {
		return fmt.Errorf("parsing uint64 %q: %w", value, err)
	}

	cl.flagSet.Uint64Var(p, flag, val, usage)

	return nil
}

func (cl *commandLine) parseInt(p *int, flag, value, usage string) error {
	if value == "" {
		cl.flagSet.IntVar(p, flag, 0, usage)

		return nil
	}

	val, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("parsing int %q: %w", value, err)
	}

	cl.flagSet.IntVar(p, flag, val, usage)

	return nil
}

func (cl *commandLine) parseInt64(p *int64, flag, value, usage string) error {
	if value == "" {
		cl.flagSet.Int64Var(p, flag, 0, usage)

		return nil
	}

	val, err := strconv.ParseInt(value, 10, 64) //nolint:gomnd
	if err != nil {
		return fmt.Errorf("parsing int64 %q: %w", value, err)
	}

	cl.flagSet.Int64Var(p, flag, val, usage)

	return nil
}

func (cl *commandLine) parseDuration(p *time.Duration, flag, value, usage string) error {
	if value == "" {
		cl.flagSet.DurationVar(p, flag, 0, usage)

		return nil
	}

	val, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("parsing duration %q: %w", value, err)
	}

	cl.flagSet.DurationVar(p, flag, val, usage)

	return nil
}

func (cl *commandLine) parseFloat64(p *float64, flag, value, usage string) error {
	if value == "" {
		cl.flagSet.Float64Var(p, flag, 0, usage)

		return nil
	}

	val, err := strconv.ParseFloat(value, 64) //nolint:gomnd
	if err != nil {
		return fmt.Errorf("parsing float64 %q: %w", value, err)
	}

	cl.flagSet.Float64Var(p, flag, val, usage)

	return nil
}

func (cl *commandLine) parseStringSlice(p *StringSlice, flag, value, usage string) error {
	if value == "" {
		*p = StringSlice{}
		cl.flagSet.Var(p, flag, usage)

		return nil
	}

	ss := &StringSlice{}

	_ = ss.Set(value)

	*p = *ss
	cl.flagSet.Var(p, flag, usage)

	return nil
}

func (cl *commandLine) parseIntSlice(p *IntSlice, flag, value, usage string) error {
	if value == "" {
		*p = IntSlice{}
		cl.flagSet.Var(p, flag, usage)

		return nil
	}

	is := &IntSlice{}

	if err := is.Set(value); err != nil {
		return err
	}

	*p = *is
	cl.flagSet.Var(p, flag, usage)

	return nil
}

func (cl *commandLine) parseURL(p *URL, flag, value, usage string) error {
	if value == "" {
		*p = URL{} //nolint:exhaustruct
		cl.flagSet.Var(p, flag, usage)

		return nil
	}

	u := &URL{} //nolint:exhaustruct

	if err := u.Set(value); err != nil {
		return err
	}

	*p = *u
	cl.flagSet.Var(p, flag, usage)

	return nil
}

func (cl *commandLine) parseTime(p *Time, flag, value, usage string) error {
	if value == "" {
		*p = Time{} //nolint:exhaustruct
		cl.flagSet.Var(p, flag, usage)

		return nil
	}

	t := &Time{} //nolint:exhaustruct

	if err := t.Set(value); err != nil {
		return err
	}

	*p = *t
	cl.flagSet.Var(p, flag, usage)

	return nil
}

func (cl *commandLine) exit(err error) error {
	if err == nil {
		return nil
	}

	switch cl.errorHandling {
	case flag.ContinueOnError:
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return err
	case flag.ExitOnError:
		if cl.help || errors.Is(err, flag.ErrHelp) {
			osExit(0)
		}

		_, _ = fmt.Fprintf(cl.output, "bee: %v\n", err)
		osExit(2) //nolint:gomnd
	case flag.PanicOnError:
		panic(err)
	}

	return nil
}
