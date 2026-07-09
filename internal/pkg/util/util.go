package util

import (
	"encoding/json"

	"github.com/alpkeskin/gotoon"
)

/*
cleans a map from emty strng or string slices with length 0
*/
func ClearMap(in map[string]interface{}) map[string]interface{} {
	for key, elem := range in {
		if str, ok := elem.(string); ok {
			if str == "" {
				delete(in, key)
			}
		}
		if strSlc, ok := elem.([]string); ok {
			if len(strSlc) == 0 {
				delete(in, key)
			}
		}
		if anySlc, ok := elem.([]interface{}); ok {
			if len(anySlc) == 0 {
				delete(in, key)
			}
		}
	}
	return in
}

// Use a drop in marshaller which outputs toon via gotoon or standard json
type OutputEncoding struct {
	useToon bool
}

func (enc *OutputEncoding) UseToon() {
	enc.useToon = true
}

func (enc *OutputEncoding) IsToon() bool {
	return enc.useToon
}

func (enc OutputEncoding) Encode(input any) (out []byte, err error) {
	if enc.useToon {
		outStr, err := gotoon.Encode(input)
		return []byte(outStr), err
	} else {
		return json.Marshal(input)
	}
}
