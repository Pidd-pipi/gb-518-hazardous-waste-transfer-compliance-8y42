// 与后端 service/waste_category.go 保持一致：许可类别字段可能使用英文逗号、
// 中文逗号或顿号分隔，且常带中文描述（如 "HW08 废矿物油，HW17 表面处理废物"）。
const HAZARD_CATEGORY = /HW\s*\d{2,3}/i;

/** 将产废单位许可类别字符串解析为去重的 HW 类别编码列表。 */
export function parsePermittedCategories(raw?: string): string[] {
	if (!raw) return [];
	const normalized = raw.replace(/[，、]/g, ',').toUpperCase();
	const seen = new Set<string>();
	const codes: string[] = [];
	for (const token of normalized.split(',')) {
		const code = extractCategory(token);
		if (code && !seen.has(code)) {
			seen.add(code);
			codes.push(code);
		}
	}
	return codes;
}

/** 从废物代码中提取类别段：HW08-900-249-08 -> HW08。 */
export function extractCategory(wasteCode?: string): string {
	if (!wasteCode) return '';
	const match = wasteCode.toUpperCase().match(HAZARD_CATEGORY);
	return match ? match[0].replace(/\s+/g, '') : '';
}

/** 判断联单废物代码是否落在许可类别集合内。 */
export function matchWasteCategory(wasteCode: string | undefined, permitted: string[]): { category: string; matched: boolean } {
	const category = extractCategory(wasteCode);
	return { category, matched: !!category && permitted.includes(category) };
}
