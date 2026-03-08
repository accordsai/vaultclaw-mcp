package catalog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func (s *Store) RemoteList(ctx context.Context, sourceID string, query string) ([]RemoteIndexItem, RemoteIndex, error) {
	source, err := s.GetSource(sourceID)
	if err != nil {
		return nil, RemoteIndex{}, err
	}
	if !source.Enabled {
		return nil, RemoteIndex{}, NewError("MCP_CATALOG_REMOTE_FETCH_FAILED", "network", "catalog source is disabled", false, map[string]any{
			"source_id": sourceID,
		})
	}
	idx, err := FetchRemoteIndex(ctx, source)
	if err != nil {
		return nil, RemoteIndex{}, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return append([]RemoteIndexItem(nil), idx.Items...), idx, nil
	}
	out := make([]RemoteIndexItem, 0, len(idx.Items))
	for _, item := range idx.Items {
		if containsAny(q, item.CookbookID, item.Version, item.Title, item.Summary, strings.Join(item.Tags, " ")) {
			out = append(out, item)
		}
	}
	return out, idx, nil
}

func (s *Store) RemoteInstall(ctx context.Context, sourceID, cookbookID, version, conflictPolicy, expectedSHA256 string) (InstallResult, error) {
	var empty InstallResult
	source, err := s.GetSource(sourceID)
	if err != nil {
		return empty, err
	}
	if !source.Enabled {
		return empty, NewError("MCP_CATALOG_REMOTE_FETCH_FAILED", "network", "catalog source is disabled", false, map[string]any{
			"source_id": sourceID,
		})
	}
	idx, err := FetchRemoteIndex(ctx, source)
	if err != nil {
		return empty, err
	}
	selected, err := selectRemoteItem(idx.Items, cookbookID, version)
	if err != nil {
		return empty, err
	}

	raw, err := downloadURL(ctx, source, selected.DownloadURL)
	if err != nil {
		return empty, err
	}
	checksum := strings.TrimSpace(expectedSHA256)
	if checksum == "" {
		checksum = strings.TrimSpace(selected.SHA256)
	}
	if checksum != "" {
		actual := SHA256Hex(raw)
		if !strings.EqualFold(checksum, actual) {
			return empty, NewError("MCP_CATALOG_REMOTE_CHECKSUM_MISMATCH", "validation", "remote cookbook checksum mismatch", false, map[string]any{
				"expected_sha256": checksum,
				"actual_sha256":   actual,
				"source_id":       sourceID,
				"cookbook_id":     selected.CookbookID,
				"version":         selected.Version,
			})
		}
	}

	var bundle Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return empty, NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "downloaded cookbook bundle is invalid json", false, map[string]any{"cause": err.Error()})
	}
	bundle = CanonicalizeBundle(bundle)
	if err := ValidateBundle(bundle); err != nil {
		return empty, err
	}
	if bundle.CookbookID != selected.CookbookID || bundle.Version != selected.Version {
		return empty, NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "downloaded bundle id/version does not match index item", false, map[string]any{
			"index_cookbook_id":  selected.CookbookID,
			"index_version":      selected.Version,
			"bundle_cookbook_id": bundle.CookbookID,
			"bundle_version":     bundle.Version,
		})
	}
	indexItem, _, err := s.UpsertBundle(bundle, conflictPolicy)
	if err != nil {
		return empty, err
	}
	contentHash, hashErr := ContentHashBundle(bundle)
	if hashErr != nil {
		return empty, NewError("MCP_INTERNAL", "internal", "failed hashing installed bundle", false, map[string]any{"cause": hashErr.Error()})
	}
	return InstallResult{
		SourceID:    sourceID,
		Selected:    selected,
		ContentHash: contentHash,
		Cookbook:    indexItem,
	}, nil
}

func FetchRemoteIndex(ctx context.Context, source SourceConfig) (RemoteIndex, error) {
	raw, err := downloadURL(ctx, source, source.IndexURL)
	if err != nil {
		return RemoteIndex{}, err
	}
	var idx RemoteIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		return RemoteIndex{}, NewError("MCP_CATALOG_REMOTE_FETCH_FAILED", "network", "failed decoding remote index", false, map[string]any{"cause": err.Error()})
	}
	idx.Type = strings.TrimSpace(idx.Type)
	if idx.SourceID == "" {
		idx.SourceID = source.SourceID
	}
	if err := ValidateRemoteIndex(idx); err != nil {
		return RemoteIndex{}, err
	}
	return idx, nil
}

func selectRemoteItem(items []RemoteIndexItem, cookbookID, version string) (RemoteIndexItem, error) {
	cookbookID = strings.TrimSpace(cookbookID)
	version = strings.TrimSpace(version)
	if cookbookID == "" {
		return RemoteIndexItem{}, NewError("MCP_VALIDATION_ERROR", "validation", "cookbook_id is required", false, map[string]any{})
	}
	candidates := make([]RemoteIndexItem, 0)
	for _, item := range items {
		if strings.TrimSpace(item.CookbookID) != cookbookID {
			continue
		}
		if version != "" && strings.TrimSpace(item.Version) != version {
			continue
		}
		candidates = append(candidates, item)
	}
	if len(candidates) == 0 {
		return RemoteIndexItem{}, NewError("MCP_CATALOG_NOT_FOUND", "validation", "cookbook version not found in remote index", false, map[string]any{
			"cookbook_id": cookbookID,
			"version":     version,
		})
	}
	if version != "" {
		return candidates[0], nil
	}
	versions := make([]string, 0, len(candidates))
	byVersion := map[string]RemoteIndexItem{}
	for _, item := range candidates {
		versions = append(versions, item.Version)
		byVersion[item.Version] = item
	}
	latest := LatestVersion(versions)
	return byVersion[latest], nil
}

func downloadURL(ctx context.Context, source SourceConfig, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(rawURL), nil)
	if err != nil {
		return nil, NewError("MCP_CATALOG_REMOTE_FETCH_FAILED", "network", "failed building remote request", false, map[string]any{"cause": err.Error()})
	}
	if source.AuthMode == AuthModeBearerEnv {
		token := strings.TrimSpace(os.Getenv(source.AuthEnvVar))
		if token == "" {
			return nil, NewError("MCP_CATALOG_REMOTE_FETCH_FAILED", "network", "bearer env var for source is empty", false, map[string]any{
				"source_id":    source.SourceID,
				"auth_env_var": source.AuthEnvVar,
			})
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, NewError("MCP_CATALOG_REMOTE_FETCH_FAILED", "network", "remote request failed", true, map[string]any{"cause": err.Error(), "url": rawURL})
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, NewError("MCP_CATALOG_REMOTE_FETCH_FAILED", "network", "remote request returned non-success status", resp.StatusCode >= 500, map[string]any{
			"url":         rawURL,
			"http_status": resp.StatusCode,
			"body":        string(body),
		})
	}
	return body, nil
}
