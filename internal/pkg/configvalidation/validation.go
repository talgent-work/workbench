package configvalidation

import (
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"
)

var (
	validatorOnce sync.Once
	validatorInst *validator.Validate
)

func Struct(value any) error {
	return instance().Struct(value)
}

func instance() *validator.Validate {
	validatorOnce.Do(func() {
		validatorInst = validator.New(validator.WithRequiredStructEnabled())
		_ = validatorInst.RegisterValidation("notblank", validateNotBlank)
		_ = validatorInst.RegisterValidation("safepath", validateSafePath)
	})
	return validatorInst
}

func validateNotBlank(fl validator.FieldLevel) bool {
	field := fl.Field()
	switch field.Kind() {
	case reflect.String:
		return strings.TrimSpace(field.String()) != ""
	default:
		return !field.IsZero()
	}
}

func validateSafePath(fl validator.FieldLevel) bool {
	field := fl.Field()
	if field.Kind() != reflect.String {
		return false
	}
	value := strings.Trim(strings.TrimSpace(field.String()), "/")
	if value == "" || strings.Contains(value, `\`) {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		segment = strings.TrimSpace(segment)
		if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, `/\`) {
			return false
		}
	}
	return true
}
