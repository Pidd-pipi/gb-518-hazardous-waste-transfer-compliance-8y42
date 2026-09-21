package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/constants"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/dto"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/model"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/repository"
)

type TransferManifestService interface {
	List(context.Context, dto.PageQuery) (repository.Page[dto.TransferManifestView], error)
	Get(context.Context, uint) (dto.TransferManifestView, error)
	Create(context.Context, dto.CreateTransferManifest, string, string) (model.TransferManifest, error)
	Update(context.Context, uint, dto.UpdateTransferManifest, string, string) (model.TransferManifest, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string) (model.TransferManifest, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
}

type transferManifestService struct {
	repository repository.TransferManifestRepository
	generators repository.WasteGeneratorRepository
	carriers   repository.CarrierProfileRepository
}

func NewTransferManifestService(repo repository.TransferManifestRepository, generators repository.WasteGeneratorRepository, carriers repository.CarrierProfileRepository) TransferManifestService {
	return &transferManifestService{repository: repo, generators: generators, carriers: carriers}
}

func (s *transferManifestService) List(ctx context.Context, query dto.PageQuery) (repository.Page[dto.TransferManifestView], error) {
	page, err := s.repository.List(ctx, query)
	if err != nil {
		return repository.Page[dto.TransferManifestView]{}, err
	}
	views := make([]dto.TransferManifestView, 0, len(page.Items))
	permitCache := make(map[string]model.WasteGenerator)
	for _, item := range page.Items {
		views = append(views, s.enrichView(ctx, item, permitCache))
	}
	return repository.Page[dto.TransferManifestView]{
		Items: views, Total: page.Total, Page: page.Page, PageSize: page.PageSize,
	}, nil
}

func (s *transferManifestService) Get(ctx context.Context, id uint) (dto.TransferManifestView, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return dto.TransferManifestView{}, err
	}
	return s.enrichView(ctx, item, nil), nil
}

func (s *transferManifestService) Create(ctx context.Context, input dto.CreateTransferManifest, actor, requestID string) (model.TransferManifest, error) {
	if err := validateTransferManifestBusinessFields(input.Code, input.Name, input.Facility, input.Owner, input.GeneratorCode, input.CarrierCode, input.WasteCode, input.Destination, input.Evidence, input.QuantityKg); err != nil {
		return model.TransferManifest{}, err
	}
	generator, err := s.generators.FindByCode(ctx, input.GeneratorCode)
	if err != nil {
		return model.TransferManifest{}, fmt.Errorf("%w: generator %s does not exist", ErrInvalidInput, input.GeneratorCode)
	}
	if _, err := s.carriers.FindByCode(ctx, input.CarrierCode); err != nil {
		return model.TransferManifest{}, fmt.Errorf("%w: carrier %s does not exist", ErrInvalidInput, input.CarrierCode)
	}
	if err := ensureWasteCodePermitted("创建联单", input.WasteCode, generator); err != nil {
		return model.TransferManifest{}, err
	}
	item := model.TransferManifest{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.TransferManifestInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		GeneratorCode: strings.ToUpper(strings.TrimSpace(input.GeneratorCode)), CarrierCode: strings.ToUpper(strings.TrimSpace(input.CarrierCode)),
		WasteCode: strings.ToUpper(strings.TrimSpace(input.WasteCode)), QuantityKg: input.QuantityKg, Destination: strings.TrimSpace(input.Destination),
		Facility: strings.TrimSpace(input.Facility), Owner: strings.TrimSpace(input.Owner),
		Category: strings.TrimSpace(input.Category), RiskLevel: input.RiskLevel,
		MetricValue: input.MetricValue, MetricUnit: strings.TrimSpace(input.MetricUnit),
		EffectiveAt: input.EffectiveAt.UTC(), Evidence: strings.TrimSpace(input.Evidence),
		RelatedCode: strings.ToUpper(strings.TrimSpace(input.RelatedCode)),
	}
	if err := s.repository.CreateAudited(ctx, &item, newAuditLog(actor, requestID, "create", "TransferManifest", "", item.Status, "created linked transfer manifest")); err != nil {
		return model.TransferManifest{}, fmt.Errorf("create 转运清单: %w", err)
	}
	return item, nil
}

func (s *transferManifestService) Update(ctx context.Context, id uint, input dto.UpdateTransferManifest, actor, requestID string) (model.TransferManifest, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.TransferManifest{}, err
	}
	if current.Status != "draft" {
		return model.TransferManifest{}, fmt.Errorf("%w: only draft manifests can be edited", ErrInvalidInput)
	}
	if err := validateTransferManifestBusinessFields(current.Code, input.Name, input.Facility, input.Owner, input.GeneratorCode, input.CarrierCode, input.WasteCode, input.Destination, input.Evidence, input.QuantityKg); err != nil {
		return model.TransferManifest{}, err
	}
	generator, err := s.generators.FindByCode(ctx, input.GeneratorCode)
	if err != nil {
		return model.TransferManifest{}, fmt.Errorf("%w: generator %s does not exist", ErrInvalidInput, input.GeneratorCode)
	}
	if _, err := s.carriers.FindByCode(ctx, input.CarrierCode); err != nil {
		return model.TransferManifest{}, fmt.Errorf("%w: carrier %s does not exist", ErrInvalidInput, input.CarrierCode)
	}
	if err := ensureWasteCodePermitted("编辑联单", input.WasteCode, generator); err != nil {
		return model.TransferManifest{}, err
	}
	current.Name = strings.TrimSpace(input.Name)
	current.GeneratorCode = strings.ToUpper(strings.TrimSpace(input.GeneratorCode))
	current.CarrierCode = strings.ToUpper(strings.TrimSpace(input.CarrierCode))
	current.WasteCode = strings.ToUpper(strings.TrimSpace(input.WasteCode))
	current.QuantityKg = input.QuantityKg
	current.Destination = strings.TrimSpace(input.Destination)
	current.Description = strings.TrimSpace(input.Description)
	current.Facility = strings.TrimSpace(input.Facility)
	current.Owner = strings.TrimSpace(input.Owner)
	current.Category = strings.TrimSpace(input.Category)
	current.RiskLevel = input.RiskLevel
	current.MetricValue = input.MetricValue
	current.MetricUnit = strings.TrimSpace(input.MetricUnit)
	current.EffectiveAt = input.EffectiveAt.UTC()
	current.Evidence = strings.TrimSpace(input.Evidence)
	current.RelatedCode = strings.ToUpper(strings.TrimSpace(input.RelatedCode))
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateAudited(ctx, id, input.ExpectedVersion, &current, newAuditLog(actor, requestID, "update", "TransferManifest", current.Status, current.Status, "updated draft manifest and evidence")); err != nil {
		return model.TransferManifest{}, fmt.Errorf("update 转运清单: %w", err)
	}
	return s.repository.Get(ctx, id)
}

func (s *transferManifestService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, requestID string) (model.TransferManifest, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.TransferManifest{}, err
	}
	target := strings.TrimSpace(input.Status)
	if !constants.CanTransition(constants.TransferManifestTransitions, current.Status, target) {
		return model.TransferManifest{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
	}
	if target == "submitted" || target == "in_transit" {
		// Re-verify against the current permit record: a narrowed permit scope
		// must reject the whole order without touching status or version.
		if err := s.validateLinkedParties(ctx, current, target); err != nil {
			return model.TransferManifest{}, err
		}
	}
	before := current.Status
	current.Status = target
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateAudited(ctx, id, input.ExpectedVersion, &current, newAuditLog(actor, requestID, "transition", "TransferManifest", before, target, input.Reason)); err != nil {
		return model.TransferManifest{}, fmt.Errorf("transition 转运清单: %w", err)
	}
	return s.repository.Get(ctx, id)
}

func (s *transferManifestService) Delete(ctx context.Context, id uint, actor, requestID string) error {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Status != "draft" {
		return fmt.Errorf("%w: submitted manifests must be retained for compliance", ErrInvalidInput)
	}
	return s.repository.DeleteAudited(ctx, id, newAuditLog(actor, requestID, "delete", "TransferManifest", current.Status, "deleted", "soft deleted draft manifest"))
}

func (s *transferManifestService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

func (s *transferManifestService) validateLinkedParties(ctx context.Context, manifest model.TransferManifest, target string) error {
	generator, err := s.generators.FindByCode(ctx, manifest.GeneratorCode)
	if err != nil {
		return fmt.Errorf("%w: linked generator is unavailable", ErrInvalidInput)
	}
	if generator.Status != "active" || !generator.PermitExpiresAt.After(time.Now().UTC()) {
		return fmt.Errorf("%w: generator permit must be active and unexpired", ErrInvalidInput)
	}
	stage := "提交前核验"
	if target == "in_transit" {
		stage = "发运前核验"
	}
	if err := ensureWasteCodePermitted(stage, manifest.WasteCode, generator); err != nil {
		return err
	}
	carrier, err := s.carriers.FindByCode(ctx, manifest.CarrierCode)
	if err != nil {
		return fmt.Errorf("%w: linked carrier is unavailable", ErrInvalidInput)
	}
	if carrier.Status != "verified" || !carrier.LicenseExpiresAt.After(time.Now().UTC()) {
		return fmt.Errorf("%w: carrier license must be verified and unexpired", ErrInvalidInput)
	}
	return nil
}

// ensureWasteCodePermitted enforces that the manifest waste code falls into a
// category listed on the generator's current permit. Permit categories may be
// separated by English/Chinese commas or enumeration commas; no row is written
// when this check fails. stage prefixes the message with the operation phase
// (e.g. 创建联单/提交前核验/发运前核验) for readable business errors.
func ensureWasteCodePermitted(stage, rawWasteCode string, generator model.WasteGenerator) error {
	wasteCode := strings.ToUpper(strings.TrimSpace(rawWasteCode))
	matched, category, permitted := PermitCategoryMatch(wasteCode, generator.WasteCategories)
	if category == "" {
		return fmt.Errorf("%w: %s时，废物代码 %s 无法识别 HW 类别，请使用如 HW08-900-249-08 的规范代码", ErrInvalidInput, stage, wasteCode)
	}
	if !matched {
		return fmt.Errorf("%w: %s时，废物代码 %s 的类别 %s 不在产废单位 %s 当前许可类别（%s）内",
			ErrInvalidInput, stage, wasteCode, category, generator.Code, describePermittedCategories(permitted))
	}
	return nil
}

// enrichView attaches the current permit categories and matching result so the
// workbench can show transferable categories without changing any stored field.
// permitCache is optional and shared within one list call to avoid repeat reads.
func (s *transferManifestService) enrichView(ctx context.Context, item model.TransferManifest, permitCache map[string]model.WasteGenerator) dto.TransferManifestView {
	view := dto.TransferManifestView{TransferManifest: item}
	generator, ok := lookupGenerator(ctx, s.generators, item.GeneratorCode, permitCache)
	if !ok {
		view.CategoryMismatch = "关联产废单位不存在，无法核验许可类别"
		return view
	}
	matched, category, permitted := PermitCategoryMatch(item.WasteCode, generator.WasteCategories)
	view.PermittedCategories = permitted
	view.CategoryMatched = matched
	switch {
	case category == "":
		view.CategoryMismatch = "废物代码无法识别 HW 类别"
	case !matched:
		view.CategoryMismatch = fmt.Sprintf("类别 %s 不在当前许可范围内", category)
	}
	return view
}

func lookupGenerator(ctx context.Context, repo repository.WasteGeneratorRepository, code string, cache map[string]model.WasteGenerator) (model.WasteGenerator, bool) {
	if cache != nil {
		if cached, ok := cache[code]; ok {
			return cached, true
		}
	}
	generator, err := repo.FindByCode(ctx, code)
	if err != nil {
		return model.WasteGenerator{}, false
	}
	if cache != nil {
		cache[code] = generator
	}
	return generator, true
}

func validateTransferManifestBusinessFields(code, name, facility, owner, generatorCode, carrierCode, wasteCode, destination, evidence string, quantityKg float64) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" || strings.TrimSpace(generatorCode) == "" || strings.TrimSpace(carrierCode) == "" || strings.TrimSpace(wasteCode) == "" || strings.TrimSpace(destination) == "" {
		return fmt.Errorf("%w: manifest identity, parties and route are required", ErrInvalidInput)
	}
	if quantityKg <= 0 || strings.TrimSpace(evidence) == "" {
		return fmt.Errorf("%w: positive waste quantity and manifest evidence are required", ErrInvalidInput)
	}
	return nil
}
