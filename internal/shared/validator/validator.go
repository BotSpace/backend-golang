package validator

import (
	"errors"
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"
)

var (
	once      sync.Once
	singleton *validator.Validate
)

// V returns a process-wide validator instance configured to use json tags
// for field names so error details map to the JSON shape clients see.
func V() *validator.Validate {
	once.Do(func() {
		v := validator.New(validator.WithRequiredStructEnabled())
		v.RegisterTagNameFunc(func(fld reflect.StructField) string {
			name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
			if name == "-" {
				return ""
			}
			if name == "" {
				return fld.Name
			}
			return name
		})
		singleton = v
	})
	return singleton
}

// Struct validates s and returns a map of fieldName -> error tag suitable for
// passing into httpx.ErrorWithDetails.
func Struct(s any) (map[string]any, error) {
	if err := V().Struct(s); err != nil {
		var ve validator.ValidationErrors
		if errors.As(err, &ve) {
			out := make(map[string]any, len(ve))
			for _, fe := range ve {
				out[fe.Field()] = fe.Tag()
			}
			return out, err
		}
		return nil, err
	}
	return nil, nil
}
