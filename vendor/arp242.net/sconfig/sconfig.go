// Package sconfig is a simple yet functional configuration file parser.
//
// See the README.markdown for an introduction.
package sconfig

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"unicode"

	"bitbucket.org/pkg/inflect"
)

// TypeHandler is passed the line split by whitespace and with the option name
// removed. It is expected to return the value to set it to or an error.
type TypeHandler func([]string) (interface{}, error)

// Handlers can be used to run custom code for a field.
//
// The map key is the name of the field in the struct.
//
// The function takes the unprocessed line split by whitespace and with the
// option name removed, it is expected to set a field directly.
//
// There is no return value, the function is expected to set any settings on the
// config struct.
type Handlers map[string]func([]string) error

var typeHandlers = make(map[string][]TypeHandler)

// RegisterType sets one or more handler functions for a type.
//
// If there are multiple types they are run in order, each passing the output to
// the next unless there is an error. This is useful to normalize or validate
// data.
func RegisterType(typeName string, fun ...TypeHandler) {
	typeHandlers[typeName] = fun
}

// readFile will read a file, strip comments, and collapse indents. This also
// deals with the special "source" command.
//
// The return value is an nested slice where the first item is the original line
// number and the second is the parsed line; for example:
//
//     [][]string{
//         []string{3, "key value"},
//         []string{9, "key2 value1 value2"},
//     }
//
// The line numbers can be used later to give more informative error messages.
//
// The input must be utf-8 encoded; other encodings are not supported.
func readFile(file string) (lines [][]string, err error) {
	fp, err := os.Open(file)
	if err != nil {
		return lines, err
	}
	defer func() { _ = fp.Close() }()

	i := 0
	no := 0
	for scanner := bufio.NewScanner(fp); scanner.Scan(); {
		no++
		line := scanner.Text()

		isIndented := len(line) > 0 && unicode.IsSpace(rune(line[0]))
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || line[0] == '#' {
			continue
		}

		line = collapseWhitespace(removeComments(line))

		if isIndented {
			if i == 0 {
				return lines, fmt.Errorf("first line can't be indented")
			}
			// Append to previous line; don't increment i since there may be
			// more indented lines.
			lines[i-1][1] += " " + line
		} else {
			// Source command
			if strings.HasPrefix(line, "source ") {
				sourced, err := readFile(line[7:])
				if err != nil {
					return nil, err
				}
				lines = append(lines, sourced...)
			} else {
				lines = append(lines, []string{fmt.Sprintf("%d", no), line})
			}
			i++
		}
	}

	return lines, nil
}

func removeComments(line string) string {
	prevcmt := 0
	for {
		cmt := strings.Index(line[prevcmt:], "#")
		if cmt < 0 {
			break
		}

		cmt += prevcmt
		prevcmt = cmt

		// Allow escaping # with \#
		if line[cmt-1] == '\\' {
			line = line[:cmt-1] + line[cmt:]
		} else {
			// Found comment
			line = line[:cmt]
			break
		}
	}

	return line
}

func collapseWhitespace(line string) string {
	nl := ""
	prevSpace := false
	for i, char := range line {
		switch {
		case char == '\\':
			// \ is escaped with \: "\\"
			if line[i-1] == '\\' {
				nl += `\`
			}
		case unicode.IsSpace(char):
			if prevSpace {
				// Escaped with \: "\ "
				if line[i-1] == '\\' {
					nl += string(char)
				}
			} else {
				prevSpace = true
				if i != len(line)-1 {
					nl += " "
				}
			}
		default:
			nl += string(char)
			prevSpace = false
		}
	}

	return nl
}

// Parse will reads file from disk and populates the given config struct c.
//
// The Handlers map can be given to customize the the behaviour; see the
// documentation on the Handlers type for details.
func Parse(c interface{}, file string, handlers Handlers) error {
	lines, err := readFile(file)
	if err != nil {
		return err
	}

	values := reflect.ValueOf(c).Elem()

	// Get list of rule names from tags
	for _, line := range lines {
		// Split by spaces
		v := strings.Split(line[1], " ")

		// Infer the field name from the key
		fieldName, err := fieldNameFromKey(v[0], values)
		if err != nil {
			return fmterr(file, line[0], v[0], err)
		}
		field := values.FieldByName(fieldName)

		// Use the handler if it exists
		if has, err := setFromHandler(fieldName, v[1:], handlers); has {
			if err != nil {
				return fmterr(file, line[0], v[0], err)
			}
			continue
		}

		// Set from typehandler
		if has, err := setFromTypeHandler(&field, v[1:]); has {
			if err != nil {
				return fmterr(file, line[0], v[0], err)
			}
			continue
		}

		// Give up :-(
		return fmterr(file, line[0], v[0], fmt.Errorf(
			"don't know how to set fields of the type %s",
			field.Type().String()))
	}

	return nil
}

func fmterr(file, line, key string, err error) error {
	return fmt.Errorf("%v line %v: error parsing %s: %v",
		file, line, key, err)
}

func fieldNameFromKey(key string, values reflect.Value) (string, error) {
	fieldName := inflect.Camelize(key)

	// TODO: Maybe find better inflect package that deals with this already?
	// This list is from golint
	acr := []string{"Api", "Ascii", "Cpu", "Css", "Dns", "Eof", "Guid", "Html",
		"Https", "Http", "Id", "Ip", "Json", "Lhs", "Qps", "Ram", "Rhs",
		"Rpc", "Sla", "Smtp", "Sql", "Ssh", "Tcp", "Tls", "Ttl", "Udp",
		"Ui", "Uid", "Uuid", "Uri", "Url", "Utf8", "Vm", "Xml", "Xsrf",
		"Xss"}
	for _, a := range acr {
		fieldName = strings.Replace(fieldName, a, strings.ToUpper(a), -1)
	}

	field := values.FieldByName(fieldName)
	if !field.CanAddr() {
		// Check plural version too; we're not too fussy
		fieldNamePlural := inflect.Pluralize(fieldName)
		field = values.FieldByName(fieldNamePlural)
		if !field.CanAddr() {
			return "", fmt.Errorf("unknown option (field %s or %s is missing)",
				fieldName, fieldNamePlural)
		}
		fieldName = fieldNamePlural
	}

	return fieldName, nil
}

func setFromHandler(fieldName string, values []string, handlers Handlers) (bool, error) {
	if handlers == nil {
		return false, nil
	}

	handler, has := handlers[fieldName]
	if !has {
		return false, nil
	}

	err := handler(values)
	if err != nil {
		return true, fmt.Errorf("%v (from handler)", err)
	}

	return true, nil
}

func setFromTypeHandler(field *reflect.Value, value []string) (bool, error) {
	handler, has := typeHandlers[field.Type().String()]
	if !has {
		return false, nil
	}

	var (
		v   interface{}
		err error
	)
	for _, h := range handler {
		v, err = h(value)
		if err != nil {
			return true, err
		}
	}
	field.Set(reflect.ValueOf(v))
	return true, nil
}

// MustParse behaves like Parse, but panics if there is an error.
func MustParse(c interface{}, file string, handlers Handlers) {
	err := Parse(c, file, handlers)
	if err != nil {
		panic(err)
	}
}

// FindConfig tries to find a config file at the usual locations (in this
// order):
//
//   $XDG_CONFIG/file (if $XDG_CONFIG is set)
//   $HOME/.config/$file
//   $HOME/.$file
//   /etc/$file
//   /usr/local/etc/$file
//   /usr/pkg/etc/$file
//   ./$file
func FindConfig(file string) string {
	file = strings.TrimLeft(file, "/")

	locations := []string{}
	if xdg := os.Getenv("XDG_CONFIG"); xdg != "" {
		locations = append(locations, strings.TrimRight(xdg, "/")+"/"+file)
	}
	if home := os.Getenv("HOME"); home != "" {
		locations = append(locations, home+"/."+file)
	}

	locations = append(locations, []string{
		"/etc/" + file,
		"/usr/local/etc/" + file,
		"/usr/pkg/etc/" + file,
		"./" + file,
	}...)

	for _, l := range locations {
		if _, err := os.Stat(l); err == nil {
			return l
		}
	}

	return ""
}

// OneValue checks if v only has a single value. It's useful to validate a
// typeHandler:
//
//   sconfig.RegisterType("*regexp.Regexp", sconfig.OneValue, func(v []string) (interface{}, error) {
//      ...
//   })
func OneValue(v []string) (interface{}, error) {
	if len(v) != 1 {
		return nil, errors.New("must have exactly one value")
	}
	return v, nil
}

// OneValues checks if v'slength is between min and max. It's useful to validate
// a typeHandler:
//
//   sconfig.RegisterType("*regexp.Regexp", sconfig.NValue(2, 3), func(v []string) (interface{}, error) {
//       ...
//   })
func NValues(min, max int) TypeHandler {
	return func(v []string) (interface{}, error) {
		switch {
		case min > 0 && len(v) < min:
			return nil, fmt.Errorf("must have more than %v values (has: %v)", min, len(v))
		case max > 0 && len(v) > max:
			return nil, fmt.Errorf("must have fewer than %v values (has: %v)", max, len(v))
		default:
			return v, nil
		}
	}
}

// The MIT License (MIT)
//
// Copyright © 2016 Martin Tournoij
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to
// deal in the Software without restriction, including without limitation the
// rights to use, copy, modify, merge, publish, distribute, sublicense, and/or
// sell copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// The software is provided "as is", without warranty of any kind, express or
// implied, including but not limited to the warranties of merchantability,
// fitness for a particular purpose and noninfringement. In no event shall the
// authors or copyright holders be liable for any claim, damages or other
// liability, whether in an action of contract, tort or otherwise, arising
// from, out of or in connection with the software or the use or other dealings
// in the software.
