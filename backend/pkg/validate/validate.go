package validate

import (
	"github.com/go-playground/validator/v10"
	"rokko.dev/pkg/apperr"
)

var v = validator.New()

func Struct(s any) error {
	if err := v.Struct(s); err != nil {
		return apperr.Invalid(err.Error())
	}
	return nil
}
