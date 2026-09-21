package service

import (
	"regexp"
	"strings"
)

// 国家危险废物名录的类别编码形如 HW08、HW49，8 位废物代码 HW08-900-249-08
// 的前导段即许可类别。核验时只比较类别段，避免把名录细码误当作新类别。
var hazardCategoryPattern = regexp.MustCompile(`HW\s*\d{2,3}`)

// categorySeparators 枚举许可类别字段允许的分隔符：英文逗号、中文逗号和顿号。
var categorySeparators = func() *strings.Replacer {
	return strings.NewReplacer("，", ",", "、", ",")
}()

// PermittedCategoryCodes 解析产废单位许可的废物类别，返回去重后的类别编码
// （如 HW08、HW17）。许可字段允许 "HW08 废矿物油,HW17 表面处理废物、HW49 其他废物"
// 这类中英文逗号、顿号混排的写法。
func PermittedCategoryCodes(categories string) []string {
	normalized := strings.ToUpper(categorySeparators.Replace(categories))
	seen := make(map[string]bool)
	codes := make([]string, 0)
	for _, token := range strings.FieldsFunc(normalized, func(r rune) bool { return r == ',' }) {
		code := extractHazardCode(token)
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		codes = append(codes, code)
	}
	return codes
}

// WasteCodeCategory 从联单废物代码中提取许可类别段。HW08-900-249-08 -> HW08，
// 直接填写 HW17 时同样识别。
func WasteCodeCategory(wasteCode string) string {
	return extractHazardCode(strings.ToUpper(strings.TrimSpace(wasteCode)))
}

// WasteCodePermitted 判断联单废物代码是否落在许可类别集合内。
func WasteCodePermitted(wasteCode string, permitted []string) (string, bool) {
	category := WasteCodeCategory(wasteCode)
	if category == "" {
		return "", false
	}
	for _, permittedCode := range permitted {
		if permittedCode == category {
			return category, true
		}
	}
	return category, false
}

// joinCategories 以顿号拼接类别列表，用于拼装可读的业务错误信息。
func joinCategories(categories []string) string {
	return strings.Join(categories, "、")
}

func extractHazardCode(value string) string {
	match := hazardCategoryPattern.FindString(strings.ToUpper(value))
	return strings.ReplaceAll(match, " ", "")
}
