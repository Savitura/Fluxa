package api

import (
	"fmt"
	"strings"

	"github.com/go-playground/validator/v10"
)

var validate = validator.New()

func Validate(v interface{}) error {
	if err := validate.Struct(v); err != nil {
		var msgs []string
		for _, e := range err.(validator.ValidationErrors) {
			msgs = append(msgs, fmt.Sprintf("%s: %s", e.Field(), e.Tag()))
		}
		return fmt.Errorf("%s", strings.Join(msgs, "; "))
	}
	return nil
}
