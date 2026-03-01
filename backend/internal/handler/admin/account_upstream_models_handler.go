package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	defaultOpenAIUpstreamBaseURL  = "https://api.openai.com"
	upstreamModelFetchTimeout     = 10 * time.Second
	upstreamModelFetchMaxBodySize = int64(1024 * 1024)
	upstreamErrorPreviewMaxChars  = 240
)

type fetchUpstreamModelsRequest struct {
	Platform string `json:"platform" binding:"required,oneof=openai sora"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key" binding:"required"`
}

type fetchUpstreamModelsResponse struct {
	Models []string `json:"models"`
}

type upstreamModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// FetchUpstreamModels handles fetching model IDs directly from upstream OpenAI/Sora compatible endpoints.
// POST /api/v1/admin/accounts/fetch-models
func (h *AccountHandler) FetchUpstreamModels(c *gin.Context) {
	var req fetchUpstreamModelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		response.BadRequest(c, "api_key is required")
		return
	}

	modelsURL, err := buildUpstreamModelsURL(req.Platform, req.BaseURL)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	models, err := requestUpstreamModelIDs(c.Request.Context(), modelsURL, apiKey)
	if err != nil {
		response.Error(c, http.StatusBadGateway, err.Error())
		return
	}

	response.Success(c, fetchUpstreamModelsResponse{Models: models})
}

func buildUpstreamModelsURL(platform, rawBaseURL string) (string, error) {
	baseURL, err := normalizeUpstreamBaseURL(platform, rawBaseURL)
	if err != nil {
		return "", err
	}

	switch platform {
	case service.PlatformOpenAI:
		baseURL = strings.TrimSuffix(baseURL, "/v1")
		return baseURL + "/v1/models", nil
	case service.PlatformSora:
		baseURL = strings.TrimSuffix(baseURL, "/sora/v1")
		baseURL = strings.TrimSuffix(baseURL, "/v1")
		return baseURL + "/sora/v1/models", nil
	default:
		return "", fmt.Errorf("unsupported platform: %s", platform)
	}
}

func normalizeUpstreamBaseURL(platform, rawBaseURL string) (string, error) {
	baseURL := strings.TrimSpace(rawBaseURL)
	if baseURL == "" {
		if platform == service.PlatformOpenAI {
			baseURL = defaultOpenAIUpstreamBaseURL
		} else {
			return "", fmt.Errorf("base_url is required")
		}
	}

	parsedURL, err := url.Parse(baseURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return "", fmt.Errorf("invalid base_url")
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "", fmt.Errorf("base_url must start with http:// or https://")
	}

	parsedURL.RawQuery = ""
	parsedURL.Fragment = ""
	return strings.TrimRight(parsedURL.String(), "/"), nil
}

func requestUpstreamModelIDs(ctx context.Context, modelsURL, apiKey string) ([]string, error) {
	requestCtx, cancel := context.WithTimeout(ctx, upstreamModelFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create upstream request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: upstreamModelFetchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to request upstream models: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, upstreamModelFetchMaxBodySize))
	if err != nil {
		return nil, fmt.Errorf("failed to read upstream response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("upstream returned status %d: %s", resp.StatusCode, compactUpstreamError(respBody))
	}

	var modelsResp upstreamModelsResponse
	if err := json.Unmarshal(respBody, &modelsResp); err != nil {
		return nil, fmt.Errorf("failed to parse upstream response: %w", err)
	}

	modelSet := make(map[string]struct{}, len(modelsResp.Data))
	for _, item := range modelsResp.Data {
		modelID := strings.TrimSpace(item.ID)
		if modelID != "" {
			modelSet[modelID] = struct{}{}
		}
	}
	if len(modelSet) == 0 {
		return nil, fmt.Errorf("upstream returned empty model list")
	}

	models := make([]string, 0, len(modelSet))
	for modelID := range modelSet {
		models = append(models, modelID)
	}
	sort.Strings(models)
	return models, nil
}

func compactUpstreamError(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "empty response body"
	}

	trimmed = strings.ReplaceAll(trimmed, "\n", " ")
	if len(trimmed) <= upstreamErrorPreviewMaxChars {
		return trimmed
	}
	return trimmed[:upstreamErrorPreviewMaxChars] + "..."
}
