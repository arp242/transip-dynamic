package sconfig_test

import (
	"errors"

	"arp242.net/sconfig"
)

func ExampleHandlers() {
	type config struct {
		Bool bool
	}

	var c config
	sconfig.MustParse(&c, "config", sconfig.Handlers{
		"Bool": func(line []string) error {
			if line[0] == "yup" {
				c.Bool = true
				return nil
			}

			return errors.New("not a bool")
		},
	})
}
