
// Hazardous waste category helpers. The backend is authoritative for permit
// verification; these mirrors keep the workbench able to parse the same
// category tokens when rendering drafts or cached records.

const CATEGORY_PATTERN = /HW\d{2,3}/i;
const SEPARATORS = /[,，、;；]+/;

/** Extract normalized HW categories from a generator permit string. */
export function parsePermitCategories(raw?: string): string[] {
  if (!raw) return [];
  const seen = new Set<string>();
  for (const segment of raw.split(SEPARATORS)) {
    const token = segment.match(CATEGORY_PATTERN)?.[0];
    if (token) seen.add(token.toUpperCase());
  }
  return [...seen].sort();
}

/** Return the HW category prefix encoded in a manifest waste code. */
export function wasteCodeCategory(code?: string): string {
  return code?.match(CATEGORY_PATTERN)?.[0]?.toUpperCase() ?? '';
}

/** Whether the waste code falls within the generator's permitted categories. */
export function permitCategoryMatch(wasteCode?: string, permitCategories?: string): { matched: boolean; category: string; permitted: string[] } {
  const permitted = parsePermitCategories(permitCategories);
  const category = wasteCodeCategory(wasteCode);
  if (!category) return { matched: false, category, permitted };
  return { matched: permitted.includes(category), category, permitted };
}
