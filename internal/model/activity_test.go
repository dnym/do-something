package model

import "testing"

func TestDecodeLoggedActivity(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`{"duration":0.4,"remaining_before":0.4,"remaining_after":0}`, true},
		{`{"duration":2,"remaining_before":1,"remaining_after":0}`, true},
		{`{"duration":1,"remaining_before":0,"remaining_after":0}`, true},
		{`null`, false}, {`{}`, false},
		{`{"duration":1,"remaining_before":0}`, false},
		{`{"duration":1,"remaining_before":null,"remaining_after":0}`, false},
		{`{"duration":0,"remaining_before":1,"remaining_after":1}`, false},
		{`{"duration":-1,"remaining_before":1,"remaining_after":2}`, false},
		{`{"duration":1,"remaining_before":3,"remaining_after":1}`, false},
		{`{"duration":1e999,"remaining_before":1,"remaining_after":0}`, false},
	} {
		_, err := DecodeLoggedActivity(tc.raw)
		if (err == nil) != tc.valid {
			t.Fatalf("%s: %v", tc.raw, err)
		}
	}
}

func TestOngoingActivityValidation(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`{"ongoing":true,"duration":0.5,"remaining_before":null,"remaining_after":null}`, true},
		{`{"ongoing":true,"duration":0.5,"remaining_before":0,"remaining_after":0}`, false},
		{`{"ongoing":false,"duration":0.5,"remaining_before":null,"remaining_after":null}`, false},
		{`{"ongoing":true,"duration":0,"remaining_before":null,"remaining_after":null}`, false},
	} {
		_, err := DecodeLoggedActivity(tc.raw)
		if (err == nil) != tc.valid {
			t.Fatal(tc, err)
		}
	}
}

func TestCostActivityValidation(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`{"duration":null,"remaining_before":null,"remaining_after":null,"cost":30,"cost_before":100,"cost_after":70}`, true},
		{`{"ongoing":true,"duration":null,"remaining_before":null,"remaining_after":null,"cost":30,"cost_before":null,"cost_after":null}`, true},
		{`{"duration":0.5,"remaining_before":1,"remaining_after":0.5,"cost":30,"cost_before":20,"cost_after":0}`, true},
		{`{"duration":null,"remaining_before":null,"remaining_after":null,"cost":null,"cost_before":null,"cost_after":null}`, false},
		{`{"duration":null,"remaining_before":null,"remaining_after":null,"cost":0,"cost_before":10,"cost_after":10}`, false},
		{`{"duration":null,"remaining_before":null,"remaining_after":null,"cost":3,"cost_before":10,"cost_after":8}`, false},
		{`{"duration":null,"remaining_before":null,"remaining_after":null,"cost":3,"cost_before":10,"cost_after":null}`, false},
	} {
		_, err := DecodeLoggedActivity(tc.raw)
		if (err == nil) != tc.valid {
			t.Fatal(tc, err)
		}
	}
}
