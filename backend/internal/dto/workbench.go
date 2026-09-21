package dto

import "github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/model"

// TransferManifestView extends the stored 转运清单 aggregate with the generator
// permit categories and a category match flag. The embedded model keeps every
// historical response field byte-compatible; the three extra fields are
// additive and omit safely when the linked generator is unavailable.
type TransferManifestView struct {
	model.TransferManifest
	PermittedCategories []string `json:"permittedCategories"`
	CategoryMatched     bool     `json:"categoryMatched"`
	CategoryMismatch    string   `json:"categoryMismatch,omitempty"`
}

// WasteGeneratorView extends the stored 产废单位 aggregate with the parsed
// category list so the workbench can render transferable categories without
// re-parsing free-form permit text on the client.
type WasteGeneratorView struct {
	model.WasteGenerator
	PermittedCategories []string `json:"permittedCategories"`
}
