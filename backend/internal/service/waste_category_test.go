package service

import (
	"reflect"
	"testing"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/model"
)

func TestParsePermitCategories(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"single category", "HW08 废矿物油", []string{"HW08"}},
		{"chinese and english commas plus dunhao", "HW08 废矿物油，HW17 表面处理废物、HW49 其他废物, HW50", []string{"HW08", "HW17", "HW49", "HW50"}},
		{"semicolon separators tolerated", "hw08;HW49；hw17", []string{"HW08", "HW17", "HW49"}},
		{"free form text ignored", "废矿物油与其他废物", []string{}},
		{"duplicates removed", "HW08,HW08、hw08", []string{"HW08"}},
		{"empty permit", "", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParsePermitCategories(tc.raw)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParsePermitCategories(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestWasteCodeCategory(t *testing.T) {
	cases := map[string]string{
		"HW08-900-249-08": "HW08",
		"hw17-336-064-17": "HW17",
		"HW499":           "HW499",
		"WASTE-UNKNOWN":   "",
		"":                "",
	}
	for input, want := range cases {
		if got := WasteCodeCategory(input); got != want {
			t.Fatalf("WasteCodeCategory(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPermitCategoryMatch(t *testing.T) {
	permit := "HW08 废矿物油, HW17 表面处理废物、HW49 其他废物"
	if matched, category, permitted := PermitCategoryMatch("HW08-900-249-08", permit); !matched || category != "HW08" || len(permitted) != 3 {
		t.Fatalf("in-scope code must match: matched=%v category=%q permitted=%v", matched, category, permitted)
	}
	if matched, category, _ := PermitCategoryMatch("HW34-900-300-34", permit); matched || category != "HW34" {
		t.Fatalf("out-of-scope code must not match: matched=%v category=%q", matched, category)
	}
	if matched, category, _ := PermitCategoryMatch("UNKNOWN-CODE", permit); matched || category != "" {
		t.Fatalf("unrecognized code must not match: matched=%v category=%q", matched, category)
	}
}

func TestEnsureWasteCodePermitted(t *testing.T) {
	generator := generatorWithPermit("WG-900", "HW08 废矿物油、HW17 表面处理废物")
	if err := ensureWasteCodePermitted("单元测试", "HW17-336-064-17", generator); err != nil {
		t.Fatalf("permitted code must pass: %v", err)
	}
	err := ensureWasteCodePermitted("单元测试", "HW49-900-041-49", generator)
	if err == nil {
		t.Fatal("out-of-scope code must return a business error")
	}
}

func generatorWithPermit(code, categories string) model.WasteGenerator {
	return model.WasteGenerator{
		BaseModel:       model.BaseModel{Code: code},
		WasteCategories: categories,
	}
}
