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

// FromBinding переводит ошибку привязки Gin (ShouldBindJSON и т. п.) в ошибку по формату §9.3.
// Ошибки программиста (не указатель, невалидируемый тип) — это 500, а не вина клиента.
func FromBinding(err error) *Error {
	var verrs validator.ValidationErrors
	if errors.As(err, &verrs) {
		fields := make([]FieldError, 0, len(verrs))
		for _, fe := range verrs {
			fields = append(fields, FieldError{Field: fieldPath(fe), Reason: reason(fe)})
		}
		return BadRequest(CodeValidation, "Некорректные данные запроса").With("fields", fields)
	}

	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	var invalidUnmarshal *json.InvalidUnmarshalError
	var invalidValidation *validator.InvalidValidationError
	switch {
	case errors.As(err, &invalidUnmarshal), errors.As(err, &invalidValidation):
		return Internal(err)
	case errors.Is(err, io.EOF):
		return BadRequest(CodeInvalidJSON, "Пустое тело запроса")
	case errors.Is(err, io.ErrUnexpectedEOF):
		return BadRequest(CodeInvalidJSON, "Тело запроса оборвано")
	case errors.As(err, &syntaxErr):
		return BadRequest(CodeInvalidJSON, "Тело запроса не является корректным JSON").With("offset", syntaxErr.Offset)
	case errors.As(err, &typeErr):
		if typeErr.Field == "" {
			// Верхний уровень не объект: массив, строка, число.
			return BadRequest(CodeInvalidJSON, "Ожидается JSON-объект")
		}
		return BadRequest(CodeValidation, "Некорректные данные запроса").
			With("fields", []FieldError{{Field: typeErr.Field, Reason: "ожидается " + jsonKind(typeErr.Type)}})
	}
	return BadRequest(CodeValidation, "Некорректные данные запроса").Wrap(err)
}

// fieldPath — путь к полю как в JSON: title, items[0].title (без имени корневой структуры).
func fieldPath(fe validator.FieldError) string {
	ns := fe.Namespace()
	if i := strings.Index(ns, "."); i >= 0 {
		return ns[i+1:]
	}
	return fe.Field()
}

// jsonKind — ожидаемый тип в терминах JSON, а не Go.
func jsonKind(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "строка"
	case reflect.Bool:
		return "булево"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "целое число"
	case reflect.Float32, reflect.Float64:
		return "число"
	case reflect.Slice, reflect.Array:
		return "массив"
	case reflect.Struct, reflect.Map:
		return "объект"
	}
	return "другой тип"
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
