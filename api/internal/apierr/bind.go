package apierr

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

// FieldError — одно нарушение в details.fields (ТЗ §9.3: «details содержит поле и причину»).
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func init() {
	// В details.fields имена полей как в JSON запроса, а не имена структур Go.
	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		v.RegisterTagNameFunc(func(fld reflect.StructField) string {
			name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
			if name == "-" || name == "" {
				return fld.Name
			}
			return name
		})
	}
}

// FromBinding переводит ошибку привязки Gin (ShouldBindJSON и т. п.) в 400 по формату §9.3.
func FromBinding(err error) *Error {
	var verrs validator.ValidationErrors
	if errors.As(err, &verrs) {
		fields := make([]FieldError, 0, len(verrs))
		for _, fe := range verrs {
			fields = append(fields, FieldError{Field: fe.Field(), Reason: reason(fe)})
		}
		return BadRequest(CodeValidation, "Некорректные данные запроса").With("fields", fields)
	}

	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.Is(err, io.EOF):
		return BadRequest(CodeInvalidJSON, "Пустое тело запроса")
	case errors.Is(err, io.ErrUnexpectedEOF):
		return BadRequest(CodeInvalidJSON, "Тело запроса оборвано")
	case errors.As(err, &syntaxErr):
		return BadRequest(CodeInvalidJSON, "Тело запроса не является корректным JSON").With("offset", syntaxErr.Offset)
	case errors.As(err, &typeErr):
		return BadRequest(CodeValidation, "Некорректные данные запроса").
			With("fields", []FieldError{{Field: typeErr.Field, Reason: "ожидается " + typeErr.Type.String()}})
	}
	return BadRequest(CodeValidation, "Некорректные данные запроса").With("reason", err.Error()).Wrap(err)
}

// reason — короткое описание правила на русском по тегу валидатора.
func reason(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "обязательное поле"
	case "min":
		return "минимум " + fe.Param()
	case "max":
		return "максимум " + fe.Param()
	case "len":
		return "длина должна быть " + fe.Param()
	case "email":
		return "некорректный email"
	case "oneof":
		return "допустимые значения: " + strings.ReplaceAll(fe.Param(), " ", ", ")
	case "gte":
		return "не меньше " + fe.Param()
	case "lte":
		return "не больше " + fe.Param()
	case "uuid":
		return "ожидается UUID"
	}
	if p := fe.Param(); p != "" {
		return fe.Tag() + "=" + p
	}
	return fe.Tag()
}
