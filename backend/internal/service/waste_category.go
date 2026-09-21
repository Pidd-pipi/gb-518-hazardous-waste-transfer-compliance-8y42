package service

import (
	"regexp"
	"sort"
	"strings"
)

// categoryPattern captures the national hazardous waste category prefix
// (HW + two or three digits, e.g. HW08) from permit entries and manifest
// waste codes such as "HW08-900-249-08".
var categoryPattern = regexp.MustCompile(`(?i)HW\d{2,3}`)

// categorySeparators covers the English comma, Chinese full-width comma and
// the Chinese enumeration comma (、), plus semicolons so permit entries copied
// from different systems all parse consistently.
var categorySeparators = regexp.MustCompile(`[` + ",，、;；" + `]+`)

// ParsePermitCategories extracts the normalized hazardous waste categories
// (HW08, HW17, ...) from a generator permit string. Free-form descriptions
// such as "废矿物油" are ignored when no category token is present and the
// result is deduplicated and sorted for stable display and error messages.
func ParsePermitCategories(raw string) []string {
	categories := make([]string, 0)
	seen := make(map[string]bool)
	for _, segment := range categorySeparators.Split(raw, -1) {
		token := categoryPattern.FindString(segment)
		if token == "" {
			continue
		}
		category := strings.ToUpper(token)
		if !seen[category] {
			seen[category] = true
			categories = append(categories, category)
		}
	}
	sort.Strings(categories)
	return categories
}

// WasteCodeCategory returns the category prefix encoded in a manifest waste
// code, or an empty string when the code does not follow the HWxx convention.
func WasteCodeCategory(code string) string {
	return strings.ToUpper(categoryPattern.FindString(code))
}

// PermitCategoryMatch reports whether the given manifest waste code falls into
// one of the categories registered on the generator permit. It also returns
// the detected code category and the permitted list so callers can render
// readable business errors and workbench hints.
func PermitCategoryMatch(wasteCode, permitCategories string) (matched bool, category string, permitted []string) {
	permitted = ParsePermitCategories(permitCategories)
	category = WasteCodeCategory(wasteCode)
	if category == "" {
		return false, "", permitted
	}
	for _, candidate := range permitted {
		if candidate == category {
			return true, category, permitted
		}
	}
	return false, category, permitted
}

// describePermittedCategories renders the current permit scope for business
// error messages, tolerating permits that only hold free-form descriptions.
func describePermittedCategories(permitted []string) string {
	if len(permitted) == 0 {
		return "许可未登记可转运废物类别"
	}
	return strings.Join(permitted, "、")
}
