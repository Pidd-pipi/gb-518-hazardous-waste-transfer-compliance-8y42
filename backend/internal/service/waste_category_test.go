package service

import (
	"reflect"
	"testing"
)

func TestPermittedCategoryCodes(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		expect []string
	}{
		{name: "single category with description", input: "HW08 废矿物油", expect: []string{"HW08"}},
		{name: "mixed separators", input: "HW08 废矿物油,HW17 表面处理废物、HW49 其他废物，HW50", expect: []string{"HW08", "HW17", "HW49", "HW50"}},
		{name: "duplicates removed", input: "hw08 油, HW08 泥、HW17", expect: []string{"HW08", "HW17"}},
		{name: "code only", input: "HW49", expect: []string{"HW49"}},
		{name: "blank", input: "  , 、 ", expect: []string{}},
		{name: "non hazard tokens ignored", input: "生活垃圾、HW08", expect: []string{"HW08"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PermittedCategoryCodes(tc.input)
			if tc.expect == nil {
				tc.expect = []string{}
			}
			if !reflect.DeepEqual(got, tc.expect) {
				t.Fatalf("PermittedCategoryCodes(%q) = %v, want %v", tc.input, got, tc.expect)
			}
		})
	}
}

func TestWasteCodePermitted(t *testing.T) {
	permitted := []string{"HW08", "HW17", "HW49"}
	cases := []struct {
		wasteCode string
		category  string
		ok        bool
	}{
		{wasteCode: "HW08-900-249-08", category: "HW08", ok: true},
		{wasteCode: "hw49-900-041-49", category: "HW49", ok: true},
		{wasteCode: "HW17", category: "HW17", ok: true},
		{wasteCode: "HW06-900-402-06", category: "HW06", ok: false},
		{wasteCode: "OTHER-CODE", category: "", ok: false},
	}
	for _, tc := range cases {
		category, ok := WasteCodePermitted(tc.wasteCode, permitted)
		if ok != tc.ok || category != tc.category {
			t.Fatalf("WasteCodePermitted(%q) = (%q,%v), want (%q,%v)", tc.wasteCode, category, ok, tc.category, tc.ok)
		}
	}
}
