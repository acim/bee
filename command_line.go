package bee

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/iancoleman/strcase"
)

// Errors.
var (
	ErrInvalidConfigType = errors.New("invalid config type")
	ErrUnsupportedType   = errors.New("type not supported")
)

type comandLine struct {
	flagSet       *flag.FlagSet
	output        io.Writer
	lookupEnvFunc func(string) (string, bool)
	name          string
	errorHandling flag.ErrorHandling
	help          bool
}

func newCommandLine(name string) *comandLine {
	a := &comandLine{ //nolint:exhaustruct
		flagSet:       flag.NewFlagSet(name, flag.ContinueOnError),
		output:        os.Stderr,
		lookupEnvFunc: os.LookupEnv,
		name:          name,
		errorHandling: flag.ExitOnError,
	}

	a.flagSet.SetOutput(a.output)

	return a
}

func (cl *comandLine) parse(config interface{}, flags []string) error {
	if err := cl.subParse(config, flags, ""); err != nil {
		return cl.exit(err)
	}

	return cl.exit(cl.flagSet.Parse(flags))
}

func (cl *comandLine) subParse(config interface{}, flags []string, prefix string) error { //nolint:cyclop
	cl.parseHelp(flags)

	v := reflect.ValueOf(config)
	t := reflect.TypeOf(config)

	if v.Kind() != reflect.Ptr || t.Elem().Kind() != reflect.Struct {
		return ErrInvalidConfigType
	}

	v = v.Elem()

	for i := 0; i < v.NumField(); i++ {
		field := t.Elem().Field(i)

		flagName := cl.flagName(field, prefix)

		envVarName := cl.envVarName(field, prefix)

		usage := cl.usage(field, envVarName, prefix)

		p := v.FieldByName(field.Name).Addr().Interface()

		// Recurse if got struct which is not of URL type
		_, oku := p.(*URL)
		_, okt := p.(*Time)

		if field.Type.Kind() == reflect.Struct && !oku && !okt {
			if err := cl.subParse(p, flags, cl.newPrefix(field, prefix)); err != nil {
				return err
			}

			continue
		}

		envVarValue, ok := cl.lookupEnvFunc(envVarName)
		if ok && !cl.help {
			if err := cl.parseValue(field.Type.Kind(), p, flagName, envVarValue, usage); err != nil {
				return fmt.Errorf("%s env: %w", field.Name, err)
			}

			continue
		}

		if err := cl.parseValue(field.Type.Kind(), p, flagName, field.Tag.Get("def"), usage); err != nil {
			return fmt.Errorf("%s def: %w", field.Name, err)
		}
	}

	return nil
}

func (*comandLine) flagName(sf reflect.StructField, prefix string) string {
	if f := sf.Tag.Get("flag"); f != "" {
		return f
	}

	n := sf.Name

	if prefix != "" {
		n = fmt.Sprintf("%s-%s", prefix, n)
	}

	return strcase.ToKebab(n)
}

func (cl *comandLine) envVarName(sf reflect.StructField, prefix string) string {
	if e := sf.Tag.Get("env"); e != "" {
		return e
	}

	n := fmt.Sprintf("%s_%s", cl.name, sf.Name)
	if prefix != "" {
		n = fmt.Sprintf("%s_%s_%s", cl.name, prefix, sf.Name)
	}

	return strcase.ToScreamingSnake(n)
}

func (*comandLine) usage(sf reflect.StructField, env string, prefix string) string {
	if u := sf.Tag.Get("help"); u != "" {
		return fmt.Sprintf("%s (env %s)", u, env)
	}

	n := sf.Name
	if prefix != "" {
		n = fmt.Sprintf("%s %s", prefix, sf.Name)
	}

	return fmt.Sprintf("%s (env %s)", strcase.ToDelimited(n, ' '), env)
}

func (cl *comandLine) parseHelp(flags []string) {
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

func (*comandLine) newPrefix(sf reflect.StructField, prefix string) string {
	if prefix != "" {
		return fmt.Sprintf("%s-%s", prefix, sf.Name)
	}

	return sf.Name
}

func (cl *comandLine) parseValue(kind reflect.Kind, varPointer interface{}, flag, value, usage string) error { //nolint:cyclop
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

func (cl *comandLine) parseBool(p *bool, flag, value, usage string) error {
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

func (cl *comandLine) parseUint(p *uint, flag, value, usage string) error {
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

func (cl *comandLine) parseUint64(p *uint64, flag, value, usage string) error {
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

func (cl *comandLine) parseInt(p *int, flag, value, usage string) error {
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

func (cl *comandLine) parseInt64(p *int64, flag, value, usage string) error {
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

func (cl *comandLine) parseDuration(p *time.Duration, flag, value, usage string) error {
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

func (cl *comandLine) parseFloat64(p *float64, flag, value, usage string) error {
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

func (cl *comandLine) parseStringSlice(p *StringSlice, flag, value, usage string) error {
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

func (cl *comandLine) parseIntSlice(p *IntSlice, flag, value, usage string) error {
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

func (cl *comandLine) parseURL(p *URL, flag, value, usage string) error {
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

func (cl *comandLine) parseTime(p *Time, flag, value, usage string) error {
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

func (cl *comandLine) exit(err error) error {
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
			os.Exit(0)
		}

		_, _ = fmt.Fprintf(cl.output, "bee: %v\n", err)
		os.Exit(2) //nolint:gomnd
	case flag.PanicOnError:
		panic(err)
	}

	return nil
}
