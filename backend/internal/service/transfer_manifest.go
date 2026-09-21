package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/constants"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/dto"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/model"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/repository"
	"gorm.io/gorm"
)

type TransferManifestService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.TransferManifest], error)
	Get(context.Context, uint) (model.TransferManifest, error)
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

func (s *transferManifestService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.TransferManifest], error) {
	page, err := s.repository.List(ctx, query)
	if err != nil {
		return page, err
	}
	permitByGenerator := make(map[string]model.WasteGenerator)
	for index := range page.Items {
		if err := s.decorateManifest(ctx, &page.Items[index], permitByGenerator); err != nil {
			return page, err
		}
	}
	return page, nil
}

func (s *transferManifestService) Get(ctx context.Context, id uint) (model.TransferManifest, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.TransferManifest{}, err
	}
	if err := s.decorateManifest(ctx, &item, make(map[string]model.WasteGenerator)); err != nil {
		return model.TransferManifest{}, err
	}
	return item, nil
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
	if err := ensureWasteCodePermitted(input.WasteCode, generator); err != nil {
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
	if err := s.decorateManifest(ctx, &item, map[string]model.WasteGenerator{generator.Code: generator}); err != nil {
		return model.TransferManifest{}, err
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
	if err := ensureWasteCodePermitted(input.WasteCode, generator); err != nil {
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
	updated, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.TransferManifest{}, err
	}
	if err := s.decorateManifest(ctx, &updated, map[string]model.WasteGenerator{generator.Code: generator}); err != nil {
		return model.TransferManifest{}, err
	}
	return updated, nil
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
		generator, err := s.validateLinkedParties(ctx, current)
		if err != nil {
			return model.TransferManifest{}, err
		}
		// 提交和发运都必须按当前许可重新核验类别：许可范围缩窄时整单拒绝，
		// 此时状态与版本保持不变（核验发生在乐观锁写入之前）。
		if err := ensureWasteCodePermitted(current.WasteCode, generator); err != nil {
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
	updated, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.TransferManifest{}, err
	}
	if err := s.decorateManifest(ctx, &updated, make(map[string]model.WasteGenerator)); err != nil {
		return model.TransferManifest{}, err
	}
	return updated, nil
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

func (s *transferManifestService) validateLinkedParties(ctx context.Context, manifest model.TransferManifest) (model.WasteGenerator, error) {
	generator, err := s.generators.FindByCode(ctx, manifest.GeneratorCode)
	if err != nil {
		return model.WasteGenerator{}, fmt.Errorf("%w: linked generator is unavailable", ErrInvalidInput)
	}
	if generator.Status != "active" || !generator.PermitExpiresAt.After(time.Now().UTC()) {
		return model.WasteGenerator{}, fmt.Errorf("%w: generator permit must be active and unexpired", ErrInvalidInput)
	}
	carrier, err := s.carriers.FindByCode(ctx, manifest.CarrierCode)
	if err != nil {
		return model.WasteGenerator{}, fmt.Errorf("%w: linked carrier is unavailable", ErrInvalidInput)
	}
	if carrier.Status != "verified" || !carrier.LicenseExpiresAt.After(time.Now().UTC()) {
		return model.WasteGenerator{}, fmt.Errorf("%w: carrier license must be verified and unexpired", ErrInvalidInput)
	}
	return generator, nil
}

// ensureWasteCodePermitted 核验联单废物代码是否落在产废单位当前许可类别中，
// 失败时携带可读的类别清单，便于前端直接展示业务错误。
func ensureWasteCodePermitted(wasteCode string, generator model.WasteGenerator) error {
	permitted := PermittedCategoryCodes(generator.WasteCategories)
	category, ok := WasteCodePermitted(wasteCode, permitted)
	if category == "" {
		return fmt.Errorf("%w: 废物代码 %s 无法识别危险废物类别（应形如 HW08 或 HW08-900-249-08）", ErrInvalidInput, strings.ToUpper(strings.TrimSpace(wasteCode)))
	}
	if !ok {
		return fmt.Errorf("%w: 废物代码 %s 属于类别 %s，不在产废单位 %s 当前许可类别（%s）范围内，联单已被拒绝且未保存", ErrInvalidInput, strings.ToUpper(strings.TrimSpace(wasteCode)), category, generator.Code, joinCategories(permitted))
	}
	return nil
}

// decorateManifest 按当前许可填充可转运类别与匹配结果；这些字段仅用于工作台
// 展示，不改变联单持久化内容和历史字段。
func (s *transferManifestService) decorateManifest(ctx context.Context, item *model.TransferManifest, cache map[string]model.WasteGenerator) error {
	generator, cached := cache[item.GeneratorCode]
	if !cached {
		found, err := s.generators.FindByCode(ctx, item.GeneratorCode)
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			// 关联单位被删除等历史数据仍需可读展示，匹配结果标记为未命中即可。
			item.PermittedCategories = []string{}
			item.CategoryMatched = false
			return nil
		}
		generator = found
		cache[item.GeneratorCode] = found
	}
	permitted := PermittedCategoryCodes(generator.WasteCategories)
	item.PermittedCategories = permitted
	matched, ok := WasteCodePermitted(item.WasteCode, permitted)
	item.MatchedCategory = matched
	item.CategoryMatched = ok
	return nil
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
