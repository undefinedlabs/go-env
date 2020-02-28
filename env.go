// Copyright 2018 Netflix, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package env provides an `env` struct field tag to marshal and unmarshal
// environment variables.
package env

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
)

var (
	// ErrInvalidValue returned when the value passed to Unmarshal is nil or not a
	// pointer to a struct.
	ErrInvalidValue = errors.New("value must be a non-nil pointer to a struct")

	// ErrUnsupportedType returned when a field with tag "env" is unsupported.
	ErrUnsupportedType = errors.New("field is an unsupported type")

	// ErrUnexportedField returned when a field with tag "env" is not exported.
	ErrUnexportedField = errors.New("field must be exported")
)

// Unmarshal parses an EnvSet and stores the result in the value pointed to by
// v. Fields that are matched in v will be deleted from EnvSet, resulting in
// an EnvSet with the remaining environment variables. If v is nil or not a
// pointer to a struct, Unmarshal returns an ErrInvalidValue.
//
// Fields tagged with "env" will have the unmarshalled EnvSet of the matching
// key from EnvSet. If the tagged field is not exported, Unmarshal returns
// ErrUnexportedField.
//
// If the field has a type that is unsupported, Unmarshal returns
// ErrUnsupportedType.
func Unmarshal(es EnvSet, v interface{}) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return ErrInvalidValue
	}

	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return ErrInvalidValue
	}

	t := rv.Type()
	for i := 0; i < rv.NumField(); i++ {
		valueField := rv.Field(i)
		switch valueField.Kind() {
		case reflect.Struct:
			if !valueField.Addr().CanInterface() {
				continue
			}

			iface := valueField.Addr().Interface()
			err := Unmarshal(es, iface)
			if err != nil {
				return err
			}
		}

		typeField := t.Field(i)
		tagValues := getTagValues(typeField, "env")
		if tagValues == nil || len(tagValues) == 0 {
			continue
		}

		if !valueField.CanSet() {
			return ErrUnexportedField
		}

		found := false
		for _, tag := range tagValues {
			envVar, ok := es[tag]
			if !ok {
				continue
			}

			err := set(es, typeField.Type, valueField, envVar)
			if err != nil {
				return err
			}
			delete(es, tag)
			found = true
			break
		}
		if !found {
			valueTypeKind := valueField.Type().Kind()
			if valueTypeKind == reflect.Ptr || valueTypeKind == reflect.Slice || valueTypeKind == reflect.Map {
				// Default value for pointers only works if the pointer is nil
				if !valueField.IsNil() {
					continue
				}
			}
			defaultValue := typeField.Tag.Get("default")
			if defaultValue == "" {
				continue
			}
			err := set(es, typeField.Type, valueField, defaultValue)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func getTagValues(field reflect.StructField, key string) []string {
	tagValue := field.Tag.Get(key)
	if tagValue == "" {
		return nil
	}
	val := strings.Split(tagValue, ",")
	for i := range val {
		val[i] = strings.TrimSpace(val[i])
	}
	return val
}

func set(es EnvSet, t reflect.Type, f reflect.Value, value string) error {
	switch t.Kind() {
	case reflect.Ptr:
		ptr := reflect.New(t.Elem())
		err := set(es, t.Elem(), ptr.Elem(), value)
		if err != nil {
			return err
		}
		f.Set(ptr)
	case reflect.String:
		value = os.Expand(value, func(s string) string { return es[s] })
		f.SetString(value)
	case reflect.Bool:
		value = os.Expand(value, func(s string) string { return es[s] })
		v, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		f.SetBool(v)
	case reflect.Int:
		value = os.Expand(value, func(s string) string { return es[s] })
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		f.SetInt(int64(v))
	case reflect.Slice:
		val := strings.Split(value, ",")
		nSlice := reflect.MakeSlice(t, 0, len(val))
		for i := range val {
			val[i] = strings.TrimSpace(val[i])
			val[i] = os.Expand(val[i], func(s string) string { return es[s] })
			rVal, err := getValue(es, t.Elem(), val[i])
			if err != nil {
				return err
			}
			nSlice = reflect.Append(nSlice, rVal)
		}
		f.Set(nSlice)
	case reflect.Map:
		val := strings.Split(value, ",")
		nMap := reflect.MakeMap(t)
		for i := range val {
			val[i] = strings.TrimSpace(val[i])
			itemArr := strings.Split(val[i], "=")
			if len(itemArr) == 2 {
				kValue, err := getValue(es, t.Key(), itemArr[0])
				if err != nil {
					continue
				}
				itemArr[1] = os.Expand(itemArr[1], func(s string) string { return es[s] })
				vValue, err := getValue(es, t.Elem(), os.ExpandEnv(itemArr[1]))
				if err != nil {
					continue
				}
				nMap.SetMapIndex(kValue, vValue)
			}
		}
		f.Set(nMap)
	default:
		return ErrUnsupportedType
	}

	return nil
}

func getValue(es EnvSet, t reflect.Type, value string) (reflect.Value, error) {
	switch t.Kind() {
	case reflect.Ptr:
		ptr := reflect.New(t.Elem())
		err := set(es, t.Elem(), ptr.Elem(), value)
		if err != nil {
			return reflect.Value{}, err
		}
		return ptr, nil
	case reflect.String:
		return reflect.ValueOf(value), nil
	case reflect.Bool:
		v, err := strconv.ParseBool(value)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(v), nil
	case reflect.Int:
		v, err := strconv.Atoi(value)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(v), nil
	default:
		return reflect.ValueOf(value), nil
	}
}

// UnmarshalFromEnviron parses an EnvSet from os.Environ and stores the result
// in the value pointed to by v. Fields that weren't matched in v are returned
// in an EnvSet with the remaining environment variables. If v is nil or not a
// pointer to a struct, UnmarshalFromEnviron returns an ErrInvalidValue.
//
// Fields tagged with "env" will have the unmarshalled EnvSet of the matching
// key from EnvSet. If the tagged field is not exported, UnmarshalFromEnviron
// returns ErrUnexportedField.
//
// If the field has a type that is unsupported, UnmarshalFromEnviron returns
// ErrUnsupportedType.
func UnmarshalFromEnviron(v interface{}) (EnvSet, error) {
	es, err := EnvironToEnvSet(os.Environ())
	if err != nil {
		return nil, err
	}

	return es, Unmarshal(es, v)
}

// Marshal returns an EnvSet of v. If v is nil or not a pointer, Marshal returns
// an ErrInvalidValue.
//
// Marshal uses fmt.Sprintf to transform encountered values to its default
// string format. Values without the "env" field tag are ignored.
//
// Nested structs are traversed recursively.
func Marshal(v interface{}) (EnvSet, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return nil, ErrInvalidValue
	}

	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return nil, ErrInvalidValue
	}

	es := make(EnvSet)
	t := rv.Type()
	for i := 0; i < rv.NumField(); i++ {
		valueField := rv.Field(i)
		switch valueField.Kind() {
		case reflect.Struct:
			if !valueField.Addr().CanInterface() {
				continue
			}

			iface := valueField.Addr().Interface()
			nes, err := Marshal(iface)
			if err != nil {
				return nil, err
			}

			for k, v := range nes {
				es[k] = v
			}
		}

		typeField := t.Field(i)
		tagValues := getTagValues(typeField, "env")
		if tagValues == nil || len(tagValues) == 0 {
			continue
		}
		tag := tagValues[0]

		if typeField.Type.Kind() == reflect.Ptr {
			for {
				if valueField.IsNil() {
					break
				}
				valueField = valueField.Elem()
				if valueField.Type().Kind() != reflect.Ptr {
					break
				}
			}
			if valueField.Type().Kind() == reflect.Ptr && valueField.IsNil() {
				continue
			}
		}

		switch valueField.Type().Kind() {
		case reflect.Slice:
			var strSlice []string
			for i := 0; i < valueField.Len(); i++ {
				item := valueField.Index(i)
				strSlice = append(strSlice, fmt.Sprintf("%v", item.Interface()))
			}
			es[tag] = strings.Join(strSlice, ", ")
		case reflect.Map:
			var strSlice []string
			iter := valueField.MapRange()
			for iter.Next() {
				k := iter.Key()
				v := iter.Value()
				strSlice = append(strSlice, fmt.Sprintf("%v=%v", k.Interface(), v.Interface()))
			}
			es[tag] = strings.Join(strSlice, ", ")
		default:
			es[tag] = fmt.Sprintf("%v", valueField.Interface())
		}
	}

	return es, nil
}
