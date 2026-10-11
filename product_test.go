package main

import (
	"reflect"
	"testing"
)

func TestProductIdentityComplete(t *testing.T) {
	v := reflect.ValueOf(product)
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() == reflect.String && f.String() == "" {
			t.Fatalf("product.json: %s is empty", v.Type().Field(i).Name)
		}
	}
	if product.StateKeys.Main == "" || product.StateKeys.Settings == "" {
		t.Fatal("product.json: stateKeys incomplete")
	}
}
