package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type upstreamModelsTestEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type upstreamModelsTestData struct {
	Models []string `json:"models"`
}

func setupFetchUpstreamModelsRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewAccountHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router.POST("/api/v1/admin/accounts/fetch-models", handler.FetchUpstreamModels)
	return router
}

func postFetchUpstreamModels(t *testing.T, router *gin.Engine, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()

	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/fetch-models", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)
	return recorder
}

func decodeFetchUpstreamEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) upstreamModelsTestEnvelope {
	t.Helper()

	var envelope upstreamModelsTestEnvelope
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	return envelope
}

func TestFetchUpstreamModels_OpenAISuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/models", r.URL.Path)
		require.Equal(t, "Bearer test-openai-key", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"gpt-4.1"},{"id":"gpt-4o"}]}`))
	}))
	defer upstream.Close()

	router := setupFetchUpstreamModelsRouter()
	rec := postFetchUpstreamModels(t, router, map[string]any{
		"platform": "openai",
		"base_url": upstream.URL,
		"api_key":  "test-openai-key",
	})

	require.Equal(t, http.StatusOK, rec.Code)
	envelope := decodeFetchUpstreamEnvelope(t, rec)
	require.Equal(t, 0, envelope.Code)

	var data upstreamModelsTestData
	require.NoError(t, json.Unmarshal(envelope.Data, &data))
	require.Equal(t, []string{"gpt-4.1", "gpt-4o"}, data.Models)
}

func TestFetchUpstreamModels_SoraUsesSoraEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/sora/v1/models", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"id":"sora2-landscape-10s"}]}`))
	}))
	defer upstream.Close()

	router := setupFetchUpstreamModelsRouter()
	rec := postFetchUpstreamModels(t, router, map[string]any{
		"platform": "sora",
		"base_url": upstream.URL + "/v1",
		"api_key":  "test-sora-key",
	})

	require.Equal(t, http.StatusOK, rec.Code)
	envelope := decodeFetchUpstreamEnvelope(t, rec)
	require.Equal(t, 0, envelope.Code)
}

func TestFetchUpstreamModels_UpstreamNon2xx(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer upstream.Close()

	router := setupFetchUpstreamModelsRouter()
	rec := postFetchUpstreamModels(t, router, map[string]any{
		"platform": "openai",
		"base_url": upstream.URL,
		"api_key":  "bad-key",
	})

	require.Equal(t, http.StatusBadGateway, rec.Code)
	envelope := decodeFetchUpstreamEnvelope(t, rec)
	require.Contains(t, envelope.Message, "status 401")
}

func TestFetchUpstreamModels_EmptyModelList(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer upstream.Close()

	router := setupFetchUpstreamModelsRouter()
	rec := postFetchUpstreamModels(t, router, map[string]any{
		"platform": "openai",
		"base_url": upstream.URL,
		"api_key":  "test-key",
	})

	require.Equal(t, http.StatusBadGateway, rec.Code)
	envelope := decodeFetchUpstreamEnvelope(t, rec)
	require.Contains(t, envelope.Message, "empty model list")
}

func TestFetchUpstreamModels_RejectsMissingSoraBaseURL(t *testing.T) {
	router := setupFetchUpstreamModelsRouter()
	rec := postFetchUpstreamModels(t, router, map[string]any{
		"platform": "sora",
		"base_url": "",
		"api_key":  "test-key",
	})

	require.Equal(t, http.StatusBadRequest, rec.Code)
	envelope := decodeFetchUpstreamEnvelope(t, rec)
	require.Equal(t, "base_url is required", envelope.Message)
}
