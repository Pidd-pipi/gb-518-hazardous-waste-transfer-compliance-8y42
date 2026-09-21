package router_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/database"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/router"
	"github.com/gin-gonic/gin"
)

// TestManifestWasteCategoryPermitVerification covers 废物类别许可核验：
// 新建/编辑草案时按许可类别拦截、提交/发运前按当前许可重新核验、许可缩窄时
// 整单拒绝且状态版本保持不变，以及工作台兼容字段透出匹配结果。
func TestManifestWasteCategoryPermitVerification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, redisClient, err := database.Open(context.Background(), testConfig(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if redisClient != nil {
		t.Fatal("test must use in-memory limiter without Redis")
	}
	engine := router.New(testConfig(t), db, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	operator := login(t, engine, "operator")

	const generatorCode = "WG-CAT-900"
	createGenerator(t, engine, operator, generatorCode, "HW08 废矿物油，HW17 表面处理废物")

	// 工作台读取产废单位：可转运类别由原始字段实时解析，历史字段仍然保留。
	response, body := request(t, engine, http.MethodGet, "/api/generators?page=1&pageSize=100&search="+generatorCode, operator, "", nil)
	assertStatus(t, response, http.StatusOK)
	if !containsJSONArray(body, "permittedCategoryCodes", []string{"HW08", "HW17"}) {
		t.Fatalf("generator workbench view must expose parsed permitted categories: %s", string(body))
	}
	if !bytes.Contains(body, []byte(`"wasteCategories":"HW08 废矿物油，HW17 表面处理废物"`)) {
		t.Fatalf("legacy wasteCategories field must remain untouched: %s", string(body))
	}

	// 许可外的废物代码不允许新建：返回可读业务错误，草案不落库。
	response, body = request(t, engine, http.MethodPost, "/api/manifests", operator, "category-create-denied",
		manifestPayloadWith("TM-CAT-BAD", generatorCode, "CP-002", "HW06-900-402-06"))
	assertStatus(t, response, http.StatusUnprocessableEntity)
	if !bytes.Contains(body, []byte("HW06")) || !bytes.Contains(body, []byte("HW08")) {
		t.Fatalf("business error must quote the offending category and the permit scope: %s", string(body))
	}
	response, searchBody := request(t, engine, http.MethodGet, "/api/manifests?page=1&pageSize=20&search=TM-CAT-BAD", operator, "", nil)
	assertStatus(t, response, http.StatusOK)
	var rejectedSearch struct {
		Data []struct {
			Code string `json:"code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(searchBody, &rejectedSearch); err != nil {
		t.Fatalf("decode rejected search: %v", err)
	}
	if len(rejectedSearch.Data) != 0 {
		t.Fatalf("rejected manifest draft must not be persisted, got %d rows", len(rejectedSearch.Data))
	}

	// 许可内的废物代码允许新建，并透出类别匹配结果。
	response, body = request(t, engine, http.MethodPost, "/api/manifests", operator, "category-create-ok",
		manifestPayloadWith("TM-CAT-OK", generatorCode, "CP-002", "HW08-900-249-08"))
	assertStatus(t, response, http.StatusCreated)
	manifest := decodeRecord(t, body)
	if !bytes.Contains(body, []byte(`"categoryMatched":true`)) || !bytes.Contains(body, []byte(`"matchedCategory":"HW08"`)) {
		t.Fatalf("created manifest must expose category match view: %s", string(body))
	}

	// 草案编辑为许可外代码同样被拒绝，联单保持原代码与版本。
	response, _ = request(t, engine, http.MethodPut, fmt.Sprintf("/api/manifests/%d", manifest.ID), operator, "category-edit-denied",
		updateManifestPayload(manifest.Version, generatorCode, "CP-002", "HW06-900-402-06"))
	assertStatus(t, response, http.StatusUnprocessableEntity)
	response, body = request(t, engine, http.MethodGet, fmt.Sprintf("/api/manifests/%d", manifest.ID), operator, "", nil)
	assertStatus(t, response, http.StatusOK)
	if !bytes.Contains(body, []byte(`"wasteCode":"HW08-900-249-08"`)) || !bytes.Contains(body, []byte(`"version":1`)) {
		t.Fatalf("rejected edit must leave waste code and version unchanged: %s", string(body))
	}

	// 合法提交。
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "category-submit", map[string]any{
		"status": "submitted", "expectedVersion": manifest.Version, "reason": "permit categories verified",
	})
	assertStatus(t, response, http.StatusOK)
	manifest = decodeRecord(t, body)
	if manifest.Status != "submitted" || manifest.Version != 2 {
		t.Fatalf("manifest should be submitted at v2, got %+v", manifest)
	}

	// 许可范围缩窄到 HW17：发运前重新核验必须整单拒绝，状态和版本保持不变。
	updateGeneratorCategories(t, engine, operator, generatorCode, "HW17 表面处理废物", 1)
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "category-ship-denied", map[string]any{
		"status": "in_transit", "expectedVersion": manifest.Version, "reason": "permit narrowed",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)
	if !bytes.Contains(body, []byte("HW08")) || !bytes.Contains(body, []byte("HW17")) {
		t.Fatalf("narrowed permit error must quote both categories: %s", string(body))
	}
	response, body = request(t, engine, http.MethodGet, fmt.Sprintf("/api/manifests/%d", manifest.ID), operator, "", nil)
	assertStatus(t, response, http.StatusOK)
	if !bytes.Contains(body, []byte(`"status":"submitted"`)) || !bytes.Contains(body, []byte(`"version":2`)) {
		t.Fatalf("rejected shipment must leave status and version untouched: %s", string(body))
	}
	if !bytes.Contains(body, []byte(`"categoryMatched":false`)) {
		t.Fatalf("workbench must surface the now-unmatched category: %s", string(body))
	}

	// 许可恢复覆盖 HW08 后，发运核验通过。
	updateGeneratorCategories(t, engine, operator, generatorCode, "HW08 废矿物油、HW17 表面处理废物", 2)
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "category-ship-ok", map[string]any{
		"status": "in_transit", "expectedVersion": manifest.Version, "reason": "permit covers HW08 again",
	})
	assertStatus(t, response, http.StatusOK)
	manifest = decodeRecord(t, body)
	if manifest.Status != "in_transit" || manifest.Version != 3 {
		t.Fatalf("manifest should be in transit at v3, got %+v", manifest)
	}
}

func createGenerator(t *testing.T, engine http.Handler, token, code, categories string) {
	t.Helper()
	response, _ := request(t, engine, http.MethodPost, "/api/generators", token, "category-generator-create", generatorPayload(code, categories))
	assertStatus(t, response, http.StatusCreated)
}

func updateGeneratorCategories(t *testing.T, engine http.Handler, token, code, categories string, expectedVersion uint) {
	t.Helper()
	payload := generatorPayload(code, categories)
	payload["expectedVersion"] = expectedVersion
	id := lookupID(t, engine, token, "generators", code)
	response, body := request(t, engine, http.MethodPut, fmt.Sprintf("/api/generators/%d", id), token, "category-generator-update", payload)
	assertStatus(t, response, http.StatusOK)
	if !bytes.Contains(body, []byte(categories)) {
		t.Fatalf("generator permit was not updated: %s", string(body))
	}
}

func lookupID(t *testing.T, engine http.Handler, token, path, code string) uint {
	t.Helper()
	response, body := request(t, engine, http.MethodGet, fmt.Sprintf("/api/%s?page=1&pageSize=100&search=%s", path, code), token, "", nil)
	assertStatus(t, response, http.StatusOK)
	var envelope struct {
		Data []struct {
			ID   uint   `json:"id"`
			Code string `json:"code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, item := range envelope.Data {
		if item.Code == code {
			return item.ID
		}
	}
	t.Fatalf("record %s not found", code)
	return 0
}

func containsJSONArray(body []byte, field string, expected []string) bool {
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Data) == 0 {
		return false
	}
	raw, exists := envelope.Data[0][field]
	if !exists {
		return false
	}
	values, ok := raw.([]any)
	if !ok || len(values) != len(expected) {
		return false
	}
	for index, value := range values {
		if value.(string) != expected[index] {
			return false
		}
	}
	return true
}

func manifestPayloadWith(code, generator, carrier, wasteCode string) map[string]any {
	payload := manifestPayload(code, carrier)
	payload["generatorCode"] = generator
	payload["wasteCode"] = wasteCode
	return payload
}

func updateManifestPayload(expectedVersion uint, generator, carrier, wasteCode string) map[string]any {
	payload := manifestPayloadWith("TM-CAT-OK", generator, carrier, wasteCode)
	delete(payload, "code")
	payload["expectedVersion"] = expectedVersion
	return payload
}

func generatorPayload(code, categories string) map[string]any {
	return map[string]any{
		"code": code, "name": "许可类别核验测试单位", "permitNumber": "PERMIT-" + code,
		"permitExpiresAt": time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339),
		"wasteCategories": categories, "description": "permit category verification fixture",
		"facility": "东区危废暂存区", "owner": "operator", "category": "危废转运",
		"riskLevel": "low", "metricValue": 10, "metricUnit": "score",
		"effectiveAt": time.Now().UTC().Format(time.RFC3339),
		"evidence":    "minio://evidence/tests/generator.pdf", "relatedCode": "REL-CAT",
	}
}
