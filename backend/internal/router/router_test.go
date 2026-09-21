package router_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/config"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/database"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/router"
	"github.com/gin-gonic/gin"
)

type apiEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
	Meta  struct {
		Total int64 `json:"total"`
	} `json:"meta"`
}

type record struct {
	ID      uint   `json:"id"`
	Code    string `json:"code"`
	Status  string `json:"status"`
	Version uint   `json:"version"`
}

func TestRBACLinkedComplianceWorkflowAndAuditing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := testConfig(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, redisClient, err := database.Open(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if redisClient != nil {
		t.Fatal("test must use in-memory limiter without Redis")
	}
	engine := router.New(cfg, db, nil, logger)

	viewer := login(t, engine, "viewer")
	operator := login(t, engine, "operator")
	reviewer := login(t, engine, "reviewer")

	response, _ := request(t, engine, http.MethodGet, "/api/generators?page=1&pageSize=10", viewer, "", nil)
	assertStatus(t, response, http.StatusOK)
	response, _ = request(t, engine, http.MethodPost, "/api/manifests", viewer, "viewer-write", manifestPayload("TM-VIEWER", "CP-002"))
	assertStatus(t, response, http.StatusForbidden)
	response, _ = request(t, engine, http.MethodGet, "/api/audits", viewer, "", nil)
	assertStatus(t, response, http.StatusForbidden)

	response, body := request(t, engine, http.MethodPost, "/api/manifests", operator, "manifest-create-valid", manifestPayload("TM-ROUTER-001", "CP-002"))
	assertStatus(t, response, http.StatusCreated)
	manifest := decodeRecord(t, body)
	if manifest.Status != "draft" || response.Header.Get("X-Request-ID") != "manifest-create-valid" {
		t.Fatalf("unexpected manifest response: %+v request-id=%q", manifest, response.Header.Get("X-Request-ID"))
	}

	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "manifest-skip", map[string]any{
		"status": "in_transit", "expectedVersion": manifest.Version, "reason": "must not skip submission",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)

	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "manifest-submit", map[string]any{
		"status": "submitted", "expectedVersion": manifest.Version, "reason": "linked permits checked",
	})
	assertStatus(t, response, http.StatusOK)
	manifest = decodeRecord(t, body)
	if manifest.Status != "submitted" || manifest.Version != 2 {
		t.Fatalf("manifest transition was not persisted: %+v", manifest)
	}

	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "manifest-stale", map[string]any{
		"status": "in_transit", "expectedVersion": uint(1), "reason": "stale client must conflict",
	})
	assertStatus(t, response, http.StatusConflict)

	// Narrowing the generator permit must reject the whole shipment while
	// leaving the manifest status and version untouched, then be re-allowed
	// once the permit scope covers the waste code again.
	generator := findGenerator(t, engine, operator, "WG-001")
	narrowed := generatorPayload("HW17 表面处理废物、HW49 其他废物")
	response, _ = request(t, engine, http.MethodPut, fmt.Sprintf("/api/generators/%d", generator.ID), operator, "generator-permit-narrow", withVersion(narrowed, generator.Version))
	assertStatus(t, response, http.StatusOK)
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "manifest-ship-narrowed", map[string]any{
		"status": "in_transit", "expectedVersion": manifest.Version, "reason": "permit scope was narrowed",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)
	if !bytes.Contains(body, []byte("HW08")) || !bytes.Contains(body, []byte("发运")) {
		t.Fatalf("narrowed permit rejection must be readable: %s", string(body))
	}
	manifest = decodeRecord(t, mustGet(t, engine, fmt.Sprintf("/api/manifests/%d", manifest.ID), operator))
	if manifest.Status != "submitted" || manifest.Version != 2 {
		t.Fatalf("rejected shipment must keep status/version: status=%q version=%d", manifest.Status, manifest.Version)
	}
	restored := generatorPayload("HW08 废矿物油，HW17 表面处理废物、HW49 其他废物")
	response, _ = request(t, engine, http.MethodPut, fmt.Sprintf("/api/generators/%d", generator.ID), operator, "generator-permit-restore", withVersion(restored, 2))
	assertStatus(t, response, http.StatusOK)
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "manifest-ship-reverified", map[string]any{
		"status": "in_transit", "expectedVersion": manifest.Version, "reason": "current permit re-verified",
	})
	assertStatus(t, response, http.StatusOK)
	manifest = decodeRecord(t, body)
	if manifest.Status != "in_transit" || manifest.Version != 3 {
		t.Fatalf("manifest shipment was not persisted after re-verification: %+v", manifest)
	}

	response, body = request(t, engine, http.MethodPost, "/api/manifests", operator, "manifest-create-out-of-scope", outOfScopePayload("TM-ROUTER-004"))
	assertStatus(t, response, http.StatusUnprocessableEntity)
	if !bytes.Contains(body, []byte("HW34")) || !bytes.Contains(body, []byte("许可类别")) {
		t.Fatalf("category violation must return a readable business error: %s", string(body))
	}
	response, body = request(t, engine, http.MethodGet, "/api/manifests?page=1&pageSize=20&search=TM-ROUTER-004", operator, "", nil)
	assertStatus(t, response, http.StatusOK)
	if envelope := decodeEnvelope(t, body); envelope.Meta.Total != 0 {
		t.Fatalf("rejected draft must not be persisted, found total=%d", envelope.Meta.Total)
	}

	response, body = request(t, engine, http.MethodPost, "/api/manifests", operator, "manifest-create-unverified", manifestPayload("TM-ROUTER-002", "CP-001"))
	assertStatus(t, response, http.StatusCreated)
	unverified := decodeRecord(t, body)
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", unverified.ID), operator, "manifest-block-unverified", map[string]any{
		"status": "submitted", "expectedVersion": unverified.Version, "reason": "must verify carrier first",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)

	response, body = request(t, engine, http.MethodPost, "/api/manifests", operator, "manifest-create-rejected", manifestPayload("TM-ROUTER-003", "CP-002"))
	assertStatus(t, response, http.StatusCreated)
	rejected := decodeRecord(t, body)
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", rejected.ID), operator, "manifest-submit-rejected", map[string]any{
		"status": "submitted", "expectedVersion": rejected.Version, "reason": "linked permits checked",
	})
	assertStatus(t, response, http.StatusOK)
	rejected = decodeRecord(t, body)
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", rejected.ID), operator, "manifest-reject", map[string]any{
		"status": "rejected", "expectedVersion": rejected.Version, "reason": "destination permit mismatch",
	})
	assertStatus(t, response, http.StatusOK)
	response, body = request(t, engine, http.MethodPost, "/api/checks", operator, "check-create-rejected", checkPayload("CC-ROUTER-002", rejected.Code))
	assertStatus(t, response, http.StatusCreated)
	rejectedCheck := decodeRecord(t, body)
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/checks/%d/transition", rejectedCheck.ID), reviewer, "rejected-manifest-pass", map[string]any{
		"status": "pass", "expectedVersion": rejectedCheck.Version, "reason": "a rejected manifest must not pass",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)

	response, body = request(t, engine, http.MethodPost, "/api/checks", operator, "check-create", checkPayload("CC-ROUTER-001", manifest.Code))
	assertStatus(t, response, http.StatusCreated)
	check := decodeRecord(t, body)
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/checks/%d/transition", check.ID), operator, "operator-decision", map[string]any{
		"status": "pass", "expectedVersion": check.Version, "reason": "operator must not decide",
	})
	assertStatus(t, response, http.StatusForbidden)

	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/checks/%d/transition", check.ID), reviewer, "reviewer-decision", map[string]any{
		"status": "pass", "expectedVersion": check.Version, "reason": "all four evidence groups verified",
	})
	assertStatus(t, response, http.StatusOK)
	check = decodeRecord(t, body)
	if check.Status != "pass" || check.Version != 2 {
		t.Fatalf("review decision was not persisted: %+v", check)
	}

	response, body = request(t, engine, http.MethodGet, "/api/audits?page=1&pageSize=100", reviewer, "audit-read", nil)
	assertStatus(t, response, http.StatusOK)
	if !bytes.Contains(body, []byte("manifest-submit")) || !bytes.Contains(body, []byte("reviewer-decision")) {
		t.Fatalf("expected request IDs in immutable audit list: %s", string(body))
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Meta.Total < 5 {
		t.Fatalf("expected audited mutations, got total=%d error=%v", envelope.Meta.Total, err)
	}

	// Workbench views must expose transferable categories and match results
	// while keeping all historical fields in the same JSON shape.
	response, body = request(t, engine, http.MethodGet, "/api/generators?page=1&pageSize=20&search=WG-001", operator, "workbench-generators", nil)
	assertStatus(t, response, http.StatusOK)
	if !bytes.Contains(body, []byte("\"permittedCategories\":[\"HW08\",\"HW17\",\"HW49\"]")) {
		t.Fatalf("generator workbench must expose parsed permitted categories: %s", string(body))
	}
	response, body = request(t, engine, http.MethodGet, "/api/manifests?page=1&pageSize=20&search=TM-ROUTER-001", operator, "workbench-manifests", nil)
	assertStatus(t, response, http.StatusOK)
	if !bytes.Contains(body, []byte("\"categoryMatched\":true")) || !bytes.Contains(body, []byte("HW08-900-249-08")) {
		t.Fatalf("manifest workbench must expose the category match result: %s", string(body))
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		AppName: "hazardous-waste-transfer-compliance-test", Environment: "test", Port: "0",
		DatabaseDriver: "sqlite", DatabaseDSN: "file:router-test?mode=memory&cache=shared",
		JWTSecret: "router-test-secret-at-least-32-characters", TokenTTL: time.Hour,
		RequestLimit: 10000, StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second,
		ReadHeaderTimeout: time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second,
	}
}

func login(t *testing.T, engine http.Handler, username string) string {
	t.Helper()
	response, body := request(t, engine, http.MethodPost, "/api/auth/login", "", "", map[string]any{
		"username": username, "password": "Admin123!",
	})
	assertStatus(t, response, http.StatusOK)
	var envelope struct {
		Data struct {
			Token string `json:"token"`
			Role  string `json:"role"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data.Token == "" || envelope.Data.Role != username {
		t.Fatalf("login %s failed: role=%q error=%v body=%s", username, envelope.Data.Role, err, string(body))
	}
	return envelope.Data.Token
}

func request(t *testing.T, engine http.Handler, method, path, token, requestID string, payload any) (*http.Response, []byte) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, body)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	engine.ServeHTTP(recorder, req)
	return recorder.Result(), recorder.Body.Bytes()
}

func assertStatus(t *testing.T, response *http.Response, expected int) {
	t.Helper()
	if response.StatusCode != expected {
		t.Fatalf("expected HTTP %d, got %d", expected, response.StatusCode)
	}
}

func decodeRecord(t *testing.T, body []byte) record {
	t.Helper()
	var envelope struct {
		Data record `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data.ID == 0 {
		t.Fatalf("decode record: %v body=%s", err, string(body))
	}
	return envelope.Data
}

func manifestPayload(code, carrier string) map[string]any {
	return map[string]any{
		"code": code, "name": "路由集成测试联单", "description": "valid linked transfer manifest",
		"generatorCode": "WG-001", "carrierCode": carrier, "wasteCode": "HW08-900-249-08", "quantityKg": 680.5,
		"destination": "合规处置中心 A", "facility": "东区危废暂存区", "owner": "operator", "category": "危废转运",
		"riskLevel": "medium", "metricValue": 68, "metricUnit": "score", "effectiveAt": time.Now().UTC().Format(time.RFC3339),
		"evidence": "minio://evidence/tests/manifest.pdf", "relatedCode": strings.ReplaceAll(code, "TM", "REL"),
	}
}

func outOfScopePayload(code string) map[string]any {
	payload := manifestPayload(code, "CP-002")
	payload["wasteCode"] = "HW34-900-300-34"
	return payload
}

func generatorPayload(categories string) map[string]any {
	return map[string]any{
		"name": "产废单位示例一", "permitNumber": "PERMIT-WG-001",
		"permitExpiresAt": time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339),
		"wasteCategories": categories, "facility": "危险废物转运合规核验区域1", "owner": "运行一组",
		"category": "常规", "riskLevel": "low", "metricValue": 12.5, "metricUnit": "unit",
		"effectiveAt": time.Now().UTC().Format(time.RFC3339), "evidence": "已完成基础证据核对", "relatedCode": "REL-518-01",
	}
}

func withVersion(payload map[string]any, version uint) map[string]any {
	payload["expectedVersion"] = version
	return payload
}

func findGenerator(t *testing.T, engine http.Handler, token, code string) record {
	t.Helper()
	response, body := request(t, engine, http.MethodGet, "/api/generators?page=1&pageSize=100&search="+code, token, "", nil)
	assertStatus(t, response, http.StatusOK)
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode generator list: %v body=%s", err, string(body))
	}
	var items []record
	if err := json.Unmarshal(envelope.Data, &items); err != nil || len(items) != 1 {
		t.Fatalf("expected exactly one generator %s, got %d: %v", code, len(items), err)
	}
	return items[0]
}

func mustGet(t *testing.T, engine http.Handler, path, token string) []byte {
	t.Helper()
	response, body := request(t, engine, http.MethodGet, path, token, "", nil)
	assertStatus(t, response, http.StatusOK)
	return body
}

func decodeEnvelope(t *testing.T, body []byte) apiEnvelope {
	t.Helper()
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, string(body))
	}
	return envelope
}

func checkPayload(code, manifest string) map[string]any {
	return map[string]any{
		"code": code, "name": "路由集成测试核验", "description": "linked compliance decision",
		"manifestCode": manifest, "checklist": "产废许可、承运资质、联单数量、处置去向", "decisionBasis": "",
		"facility": "复核中心", "owner": "reviewer", "category": "联单复核", "riskLevel": "medium",
		"metricValue": 92, "metricUnit": "score", "effectiveAt": time.Now().UTC().Format(time.RFC3339),
		"evidence": "minio://evidence/tests/check.pdf", "relatedCode": manifest,
	}
}
