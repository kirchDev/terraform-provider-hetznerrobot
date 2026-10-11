package client

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// The documentation is inconsistent about scalar types — server_number is an
// Integer in one table and a String in the next, a subnet mask is 29 in a list
// and "29" in the MAC and cancellation answers, prices are strings. These types
// accept every spelling, so a field nobody configures can never fail a read.

// FlexInt decodes a JSON number, a numeric string or null. Valid is false for
// null (and for a string that is not a number).
type FlexInt struct {
	Value int64
	Valid bool
}

func (f *FlexInt) UnmarshalJSON(b []byte) error {
	*f = FlexInt{}
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		*f = FlexInt{Value: n, Valid: true}
		return nil
	}
	if fl, err := strconv.ParseFloat(s, 64); err == nil {
		*f = FlexInt{Value: int64(fl), Valid: true}
	}
	return nil
}

// FlexString decodes any JSON scalar as a string. Valid is false for null.
type FlexString struct {
	Value string
	Valid bool
}

func (f *FlexString) UnmarshalJSON(b []byte) error {
	*f = FlexString{}
	t := bytes.TrimSpace(b)
	if len(t) == 0 || string(t) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(t, &s); err == nil {
		*f = FlexString{Value: s, Valid: true}
		return nil
	}
	*f = FlexString{Value: string(t), Valid: true}
	return nil
}

// FlexFloat decodes a JSON number or numeric string. Valid is false for null.
type FlexFloat struct {
	Value float64
	Valid bool
}

func (f *FlexFloat) UnmarshalJSON(b []byte) error {
	*f = FlexFloat{}
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return nil
	}
	if fl, err := strconv.ParseFloat(s, 64); err == nil {
		*f = FlexFloat{Value: fl, Valid: true}
	}
	return nil
}

// StringOrList decodes the boot and order fields that are an array of the
// available choices while inactive and the chosen string once active
// ("os": ["linux", "vkvm"] vs "os": "linux"). Numbers are kept as strings.
type StringOrList struct {
	List   []string
	Single string
	// IsList tells the two shapes apart; null is an empty list.
	IsList bool
}

func (s *StringOrList) UnmarshalJSON(b []byte) error {
	*s = StringOrList{}
	t := bytes.TrimSpace(b)
	if len(t) == 0 || string(t) == "null" {
		s.IsList = true
		s.List = []string{}
		return nil
	}
	if t[0] == '[' {
		var items []FlexString
		if err := json.Unmarshal(t, &items); err != nil {
			return err
		}
		s.IsList = true
		s.List = make([]string, 0, len(items))
		for _, it := range items {
			if it.Valid {
				s.List = append(s.List, it.Value)
			}
		}
		return nil
	}
	var one FlexString
	if err := json.Unmarshal(t, &one); err != nil {
		return err
	}
	s.Single = one.Value
	return nil
}

// Values returns the list, or the single value as a one-element list.
func (s StringOrList) Values() []string {
	if s.IsList {
		return s.List
	}
	if s.Single == "" {
		return []string{}
	}
	return []string{s.Single}
}
