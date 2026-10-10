package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
)

type testStructValid struct {
	Name  string `validate:"required"`
	Email string `validate:"required,email"`
	Age   int    `validate:"required,gte=18"`
}

func TestValidate_ValidStruct(t *testing.T) {
	s := testStructValid{
		Name:  "John Doe",
		Email: "john@example.com",
		Age:   25,
	}

	err := Validate(s)
	if err != nil {
		t.Fatalf("expected no error for valid struct, got: %v", err)
	}
}

func TestValidate_InvalidStruct_ValidationErrors(t *testing.T) {
	s := testStructValid{
		Name:  "",
		Email: "invalid-email",
		Age:   10,
	}

	err := Validate(s)
	if err == nil {
		t.Fatal("expected error for invalid struct")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "Name: required") {
		t.Errorf("expected 'Name: required' in error, got: %s", errStr)
	}
	if !strings.Contains(errStr, "Email: email") {
		t.Errorf("expected 'Email: email' in error, got: %s", errStr)
	}
	if !strings.Contains(errStr, "Age: gte") {
		t.Errorf("expected 'Age: gte' in error, got: %s", errStr)
	}
}

func TestValidate_NilPointer_InvalidValidationError(t *testing.T) {
	var s *testStructValid = nil

	err := Validate(s)
	if err == nil {
		t.Fatal("expected error for nil pointer")
	}

	var invalidErr *validator.InvalidValidationError
	if !errors.As(err, &invalidErr) {
		t.Errorf("expected InvalidValidationError to be wrapped, got: %v", err)
	}
}

func TestValidate_NonStruct_InvalidValidationError(t *testing.T) {
	err := Validate("not a struct")
	if err == nil {
		t.Fatal("expected error for non-struct input")
	}

	var invalidErr *validator.InvalidValidationError
	if !errors.As(err, &invalidErr) {
		t.Errorf("expected InvalidValidationError to be wrapped, got: %v", err)
	}
}

func TestValidate_Map_InvalidValidationError(t *testing.T) {
	err := Validate(map[string]string{"key": "value"})
	if err == nil {
		t.Fatal("expected error for map input")
	}

	var invalidErr *validator.InvalidValidationError
	if !errors.As(err, &invalidErr) {
		t.Errorf("expected InvalidValidationError to be wrapped, got: %v", err)
	}
}

func TestValidate_Slice_InvalidValidationError(t *testing.T) {
	err := Validate([]string{"a", "b"})
	if err == nil {
		t.Fatal("expected error for slice input")
	}

	var invalidErr *validator.InvalidValidationError
	if !errors.As(err, &invalidErr) {
		t.Errorf("expected InvalidValidationError to be wrapped, got: %v", err)
	}
}

func TestValidate_PointerToValidStruct(t *testing.T) {
	s := &testStructValid{
		Name:  "Jane Doe",
		Email: "jane@example.com",
		Age:   30,
	}

	err := Validate(s)
	if err != nil {
		t.Fatalf("expected no error for valid pointer to struct, got: %v", err)
	}
}

func TestValidate_PointerToInvalidStruct(t *testing.T) {
	s := &testStructValid{
		Name:  "",
		Email: "bad",
		Age:   5,
	}

	err := Validate(s)
	if err == nil {
		t.Fatal("expected error for invalid pointer to struct")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "Name: required") {
		t.Errorf("expected 'Name: required' in error, got: %s", errStr)
	}
}
