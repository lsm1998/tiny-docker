package image

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"time"
)

const (
	defaultRegistryHost = "registry-1.docker.io"
	defaultDataRoot     = "/var/lib/tinydocker"
	storageDriver       = "overlay2"
)

var acceptedManifestTypes = []string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.docker.distribution.manifest.v1+json",
	"application/json",
}

type imageReference struct {
	Original   string
	Registry   string
	Repository string
	Reference  string
}

type registryClient struct {
	baseURL string
	client  *http.Client
	tokens  map[string]string
}

type descriptor struct {
	MediaType string   `json:"mediaType"`
	Digest    string   `json:"digest"`
	Size      int64    `json:"size"`
	URLs      []string `json:"urls,omitempty"`
	Platform  struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
		Variant      string `json:"variant,omitempty"`
	} `json:"platform,omitempty"`
}

type imageManifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        descriptor   `json:"config"`
	Layers        []descriptor `json:"layers"`
}

type manifestList struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Manifests     []descriptor `json:"manifests"`
}

type imageConfigBlob struct {
	Config struct {
		Entrypoint []string `json:"Entrypoint"`
		Cmd        []string `json:"Cmd"`
		Env        []string `json:"Env"`
		WorkingDir string   `json:"WorkingDir"`
	} `json:"config"`
}

type bearerChallenge struct {
	Realm   string
	Service string
	Scope   string
}

type resolvedManifest struct {
	Digest    string
	MediaType string
	Payload   []byte
	Manifest  imageManifest
}

type repositoryIndex struct {
	Images map[string]storedImage `json:"images"`
}

type storedImage struct {
	Name           string        `json:"name"`
	Registry       string        `json:"registry"`
	Repository     string        `json:"repository"`
	Reference      string        `json:"reference"`
	ManifestDigest string        `json:"manifest_digest"`
	ConfigDigest   string        `json:"config_digest"`
	ImageID        string        `json:"image_id"`
	TopLayer       string        `json:"top_layer"`
	Layers         []storedLayer `json:"layers"`
	Entrypoint     []string      `json:"entrypoint,omitempty"`
	Cmd            []string      `json:"cmd,omitempty"`
	Env            []string      `json:"env,omitempty"`
	WorkingDir     string        `json:"working_dir,omitempty"`
	PulledAt       string        `json:"pulled_at"`
}

type storedLayer struct {
	Digest  string `json:"digest"`
	DiffID  string `json:"diff_id"`
	ChainID string `json:"chain_id"`
	CacheID string `json:"cache_id"`
	Size    int64  `json:"size"`
}

func Pull(rawRef string) error {
	ref, err := parseImageReference(rawRef)
	if err != nil {
		return err
	}
	if err := ensurePullLayout(); err != nil {
		return err
	}

	client := newRegistryClient(ref.Registry)
	manifest, err := client.fetchResolvedManifest(ref)
	if err != nil {
		return err
	}
	config, configPayload, err := client.fetchImageConfig(ref, manifest.Manifest.Config.Digest)
	if err != nil {
		return err
	}

	if err := storeManifest(ref, manifest); err != nil {
		return err
	}
	if err := storeConfigBlob(manifest.Manifest.Config.Digest, configPayload); err != nil {
		return err
	}

	layers := make([]storedLayer, 0, len(manifest.Manifest.Layers))
	parentChainID := ""
	for i, layer := range manifest.Manifest.Layers {
		fmt.Printf("pulling layer %d/%d %s\n", i+1, len(manifest.Manifest.Layers), shortDigest(layer.Digest))
		stored, err := pullLayer(client, ref, layer, parentChainID)
		if err != nil {
			return err
		}
		layers = append(layers, stored)
		parentChainID = stored.ChainID
	}

	image := storedImage{
		Name:           normalizedImageName(ref),
		Registry:       ref.Registry,
		Repository:     ref.Repository,
		Reference:      ref.Reference,
		ManifestDigest: manifest.Digest,
		ConfigDigest:   manifest.Manifest.Config.Digest,
		ImageID:        digestHexPart(manifest.Manifest.Config.Digest),
		TopLayer:       parentChainID,
		Layers:         layers,
		Entrypoint:     append([]string(nil), config.Config.Entrypoint...),
		Cmd:            append([]string(nil), config.Config.Cmd...),
		Env:            append([]string(nil), config.Config.Env...),
		WorkingDir:     strings.TrimSpace(config.Config.WorkingDir),
		PulledAt:       time.Now().UTC().Format(time.RFC3339),
	}
	if err := upsertRepositoryIndex(image); err != nil {
		return err
	}

	fmt.Printf("pulled %s\n", image.Name)
	return nil
}

// parseImageReference
func parseImageReference(raw string) (imageReference, error) {
	ref := strings.TrimSpace(raw)
	if ref == "" {
		return imageReference{}, errors.New("image reference cannot be empty")
	}
	if strings.Contains(ref, "://") {
		return imageReference{}, errors.New("image reference must not include a URL scheme")
	}

	registry := defaultRegistryHost
	remainder := ref

	if slash := strings.Index(remainder, "/"); slash > 0 {
		first := remainder[:slash]
		if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
			registry = first
			remainder = remainder[slash+1:]
		}
	}
	if registry == "docker.io" || registry == "index.docker.io" {
		registry = defaultRegistryHost
	}

	repository, reference := splitRepositoryReference(remainder)
	if repository == "" {
		return imageReference{}, fmt.Errorf("invalid image reference %q", raw)
	}
	if registry == defaultRegistryHost && !strings.Contains(repository, "/") {
		repository = "library/" + repository
	}

	return imageReference{
		Original:   raw,
		Registry:   registry,
		Repository: repository,
		Reference:  reference,
	}, nil
}

func splitRepositoryReference(input string) (repository string, reference string) {
	if repo, digest, ok := strings.Cut(input, "@"); ok {
		if repo == "" || digest == "" {
			return "", ""
		}
		return repo, digest
	}

	reference = "latest"
	slash := strings.LastIndex(input, "/")
	colon := strings.LastIndex(input, ":")
	if colon > slash {
		reference = input[colon+1:]
		input = input[:colon]
	}
	return input, reference
}

func normalizedImageName(ref imageReference) string {
	name := trimLibraryPrefix(ref.Repository)
	if strings.HasPrefix(ref.Reference, "sha256:") {
		return name + "@" + ref.Reference
	}
	return name + ":" + ref.Reference
}

func trimLibraryPrefix(repository string) string {
	return strings.TrimPrefix(repository, "library/")
}

func newRegistryClient(registry string) *registryClient {
	return &registryClient{
		baseURL: "https://" + registry,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
		tokens: make(map[string]string),
	}
}

func (c *registryClient) fetchResolvedManifest(ref imageReference) (resolvedManifest, error) {
	payload, mediaType, digest, err := c.fetchManifestBytes(ref, ref.Reference)
	if err != nil {
		return resolvedManifest{}, err
	}

	switch manifestMediaType(payload, mediaType) {
	case "application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json":
		var index manifestList
		if err := json.Unmarshal(payload, &index); err != nil {
			return resolvedManifest{}, err
		}
		selected, err := selectPlatformManifest(index.Manifests)
		if err != nil {
			return resolvedManifest{}, err
		}
		payload, mediaType, digest, err = c.fetchManifestBytes(ref, selected.Digest)
		if err != nil {
			return resolvedManifest{}, err
		}
	}

	var manifest imageManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return resolvedManifest{}, err
	}
	if len(manifest.Layers) == 0 {
		return resolvedManifest{}, errors.New("image manifest has no layers")
	}

	return resolvedManifest{
		Digest:    digest,
		MediaType: mediaType,
		Payload:   payload,
		Manifest:  manifest,
	}, nil
}

func (c *registryClient) fetchManifestBytes(ref imageReference, target string) ([]byte, string, string, error) {
	u := fmt.Sprintf("%s/v2/%s/manifests/%s", c.baseURL, ref.Repository, target)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, "", "", err
	}
	for _, mediaType := range acceptedManifestTypes {
		req.Header.Add("Accept", mediaType)
	}

	resp, err := c.do(req, repositoryPullScope(ref.Repository))
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", "", registryStatusError("fetch manifest", resp)
	}

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", "", err
	}
	digest := strings.TrimSpace(resp.Header.Get("Docker-Content-Digest"))
	if digest == "" {
		digest = digestBytes(payload)
	}
	mediaType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	return payload, mediaType, digest, nil
}

func (c *registryClient) fetchBlob(ref imageReference, digest string) (io.ReadCloser, error) {
	u := fmt.Sprintf("%s/v2/%s/blobs/%s", c.baseURL, ref.Repository, digest)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.do(req, repositoryPullScope(ref.Repository))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, registryStatusError("download blob", resp)
	}
	return resp.Body, nil
}

func (c *registryClient) fetchBlobBytes(ref imageReference, digest string) ([]byte, error) {
	body, err := c.fetchBlob(ref, digest)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(body)
}

func (c *registryClient) fetchImageConfig(ref imageReference, digest string) (imageConfigBlob, []byte, error) {
	payload, err := c.fetchBlobBytes(ref, digest)
	if err != nil {
		return imageConfigBlob{}, nil, err
	}

	var config imageConfigBlob
	if err := json.Unmarshal(payload, &config); err != nil {
		return imageConfigBlob{}, nil, fmt.Errorf("decode image config %s: %w", digest, err)
	}
	return config, payload, nil
}

func (c *registryClient) do(req *http.Request, scope string) (*http.Response, error) {
	resp, err := c.doOnce(req, scope)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	challenge, err := parseBearerChallenge(resp.Header.Get("WWW-Authenticate"))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if challenge.Scope == "" {
		challenge.Scope = scope
	}
	token, err := c.fetchToken(challenge)
	if err != nil {
		return nil, err
	}
	c.tokens[scope] = token
	return c.doOnce(req, scope)
}

func (c *registryClient) doOnce(req *http.Request, scope string) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	if token := c.tokens[scope]; token != "" {
		cloned.Header.Set("Authorization", "Bearer "+token)
	}
	return c.client.Do(cloned)
}

func (c *registryClient) fetchToken(challenge bearerChallenge) (string, error) {
	if challenge.Realm == "" {
		return "", errors.New("registry auth challenge is missing realm")
	}

	values := url.Values{}
	if challenge.Service != "" {
		values.Set("service", challenge.Service)
	}
	if challenge.Scope != "" {
		values.Set("scope", challenge.Scope)
	}

	tokenURL := challenge.Realm
	if encoded := values.Encode(); encoded != "" {
		tokenURL += "?" + encoded
	}

	req, err := http.NewRequest(http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", registryStatusError("fetch token", resp)
	}

	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Token != "" {
		return payload.Token, nil
	}
	if payload.AccessToken != "" {
		return payload.AccessToken, nil
	}
	return "", errors.New("registry token response did not contain a token")
}

func repositoryPullScope(repository string) string {
	return "repository:" + repository + ":pull"
}

func parseBearerChallenge(header string) (bearerChallenge, error) {
	if header == "" {
		return bearerChallenge{}, errors.New("registry requires authentication but did not return a challenge")
	}
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return bearerChallenge{}, fmt.Errorf("unsupported registry auth challenge %q", header)
	}

	challenge := bearerChallenge{}
	parts := strings.Split(header[len("Bearer "):], ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"`)
		switch strings.ToLower(key) {
		case "realm":
			challenge.Realm = value
		case "service":
			challenge.Service = value
		case "scope":
			challenge.Scope = value
		}
	}
	return challenge, nil
}

func manifestMediaType(payload []byte, headerMediaType string) string {
	if headerMediaType != "" {
		return headerMediaType
	}
	var media struct {
		MediaType string `json:"mediaType"`
	}
	if err := json.Unmarshal(payload, &media); err != nil {
		return ""
	}
	return media.MediaType
}

func selectPlatformManifest(manifests []descriptor) (descriptor, error) {
	wantOS := "linux"
	wantArch := goruntime.GOARCH

	for _, manifest := range manifests {
		if manifest.Platform.OS == wantOS && manifest.Platform.Architecture == wantArch {
			return manifest, nil
		}
	}
	for _, manifest := range manifests {
		if manifest.Platform.OS == wantOS && manifest.Platform.Architecture == "amd64" {
			return manifest, nil
		}
	}
	return descriptor{}, fmt.Errorf("no manifest found for platform %s/%s", wantOS, wantArch)
}

func registryStatusError(action string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = resp.Status
	}
	return fmt.Errorf("%s: %s", action, message)
}

func shortDigest(digest string) string {
	if len(digest) <= 18 {
		return digest
	}
	return digest[:18]
}

func pullLayer(client *registryClient, ref imageReference, layer descriptor, parentChainID string) (storedLayer, error) {
	blobPath, err := ensureLayerBlob(client, ref, layer.Digest)
	if err != nil {
		return storedLayer{}, err
	}

	diffID, err := readDiffIDMapping(layer.Digest)
	if err != nil {
		return storedLayer{}, err
	}
	if diffID == "" {
		diffID, err = computeLayerDiffID(blobPath)
		if err != nil {
			return storedLayer{}, err
		}
		if err := writeDiffIDMapping(layer.Digest, diffID); err != nil {
			return storedLayer{}, err
		}
	}

	chainID := composeChainID(parentChainID, diffID)
	if existing, ok, err := loadStoredLayer(chainID, layer.Digest, layer.Size); err != nil {
		return storedLayer{}, err
	} else if ok {
		return existing, nil
	}

	unpackedDiffID, cacheID, err := unpackLayer(blobPath, layer.Digest)
	if err != nil {
		return storedLayer{}, err
	}
	if unpackedDiffID != diffID {
		return storedLayer{}, fmt.Errorf("layer %s diffID mismatch: want %s got %s", layer.Digest, diffID, unpackedDiffID)
	}

	stored := storedLayer{
		Digest:  layer.Digest,
		DiffID:  unpackedDiffID,
		ChainID: chainID,
		CacheID: cacheID,
		Size:    layer.Size,
	}
	if err := writeLayerMetadata(stored, parentChainID); err != nil {
		return storedLayer{}, err
	}
	return stored, nil
}

func ensureLayerBlob(client *registryClient, ref imageReference, digest string) (string, error) {
	target := blobPath(digest)
	if _, err := os.Stat(target); err == nil {
		return target, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	body, err := client.fetchBlob(ref, digest)
	if err != nil {
		return "", err
	}
	defer body.Close()

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	tempPath := target + ".tmp"
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(file, body); err != nil {
		file.Close()
		_ = os.Remove(tempPath)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", err
	}
	if err := os.Rename(tempPath, target); err != nil {
		_ = os.Remove(tempPath)
		return "", err
	}
	return target, nil
}

func unpackLayer(blobPath, compressedDigest string) (string, string, error) {
	cacheID, err := randomHex(32)
	if err != nil {
		return "", "", err
	}
	tempDir := filepath.Join(overlayRoot(), "."+cacheID+".tmp")
	diffDir := filepath.Join(tempDir, "diff")
	if err := os.MkdirAll(diffDir, 0o755); err != nil {
		return "", "", err
	}

	file, err := os.Open(blobPath)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return "", "", err
	}
	diffID, err := extractLayer(file, diffDir)
	file.Close()
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return "", "", err
	}

	if err := writeDiffIDMapping(compressedDigest, diffID); err != nil {
		_ = os.RemoveAll(tempDir)
		return "", "", err
	}

	finalDir := filepath.Join(overlayRoot(), cacheID)
	if err := os.Rename(tempDir, finalDir); err != nil {
		_ = os.RemoveAll(tempDir)
		return "", "", err
	}
	return diffID, cacheID, nil
}

func computeLayerDiffID(blobPath string) (string, error) {
	file, err := os.Open(blobPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	digest := sha256.New()
	tarReader, closeFn, err := newLayerTarReader(file, digest)
	if err != nil {
		return "", err
	}
	if closeFn != nil {
		defer closeFn()
	}

	for {
		_, err := tarReader.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
			}
			return "", err
		}
		if _, err := io.Copy(io.Discard, tarReader); err != nil {
			return "", err
		}
	}
}

func loadStoredLayer(chainID, digest string, size int64) (storedLayer, bool, error) {
	layerDir := layerDBDir(chainID)
	cacheIDBytes, err := os.ReadFile(filepath.Join(layerDir, "cache-id"))
	if errors.Is(err, os.ErrNotExist) {
		return storedLayer{}, false, nil
	}
	if err != nil {
		return storedLayer{}, false, err
	}

	diffIDBytes, err := os.ReadFile(filepath.Join(layerDir, "diff"))
	if err != nil {
		return storedLayer{}, false, err
	}

	return storedLayer{
		Digest:  digest,
		DiffID:  strings.TrimSpace(string(diffIDBytes)),
		ChainID: chainID,
		CacheID: strings.TrimSpace(string(cacheIDBytes)),
		Size:    size,
	}, true, nil
}

func writeLayerMetadata(layer storedLayer, parentChainID string) error {
	layerDir := layerDBDir(layer.ChainID)
	if err := os.MkdirAll(layerDir, 0o755); err != nil {
		return err
	}
	files := map[string]string{
		"cache-id": layer.CacheID,
		"diff":     layer.DiffID,
		"digest":   layer.Digest,
		"size":     fmt.Sprintf("%d", layer.Size),
	}
	if parentChainID != "" {
		files["parent"] = parentChainID
	}
	for name, value := range files {
		if err := writeFileAtomic(filepath.Join(layerDir, name), []byte(value+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func storeManifest(ref imageReference, manifest resolvedManifest) error {
	type manifestRecord struct {
		Registry   string          `json:"registry"`
		Repository string          `json:"repository"`
		Reference  string          `json:"reference"`
		Digest     string          `json:"digest"`
		MediaType  string          `json:"media_type"`
		PulledAt   time.Time       `json:"pulled_at"`
		Payload    json.RawMessage `json:"payload"`
	}

	record := manifestRecord{
		Registry:   ref.Registry,
		Repository: ref.Repository,
		Reference:  ref.Reference,
		Digest:     manifest.Digest,
		MediaType:  manifest.MediaType,
		PulledAt:   time.Now().UTC(),
		Payload:    append([]byte(nil), manifest.Payload...),
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(manifestPath(manifest.Digest), data, 0o644)
}

func storeConfigBlob(digest string, payload []byte) error {
	return writeFileAtomic(configPath(digest), payload, 0o644)
}

func upsertRepositoryIndex(image storedImage) error {
	index, err := loadRepositoryIndex()
	if err != nil {
		return err
	}
	if index.Images == nil {
		index.Images = make(map[string]storedImage)
	}
	index.Images[image.Name] = image

	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(repositoriesPath(), data, 0o644)
}

func loadRepositoryIndex() (repositoryIndex, error) {
	path := repositoriesPath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return repositoryIndex{Images: make(map[string]storedImage)}, nil
	}
	if err != nil {
		return repositoryIndex{}, err
	}

	var index repositoryIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return repositoryIndex{}, err
	}
	if index.Images == nil {
		index.Images = make(map[string]storedImage)
	}
	return index, nil
}

func readDiffIDMapping(digest string) (string, error) {
	data, err := os.ReadFile(diffIDMappingPath(digest))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func writeDiffIDMapping(digest, diffID string) error {
	return writeFileAtomic(diffIDMappingPath(digest), []byte(diffID+"\n"), 0o644)
}

func ensurePullLayout() error {
	for _, dir := range []string{
		dataRoot(),
		filepath.Join(dataRoot(), "image", storageDriver, "imagedb", "content", "sha256"),
		filepath.Join(dataRoot(), "image", storageDriver, "distribution", "diffid-by-digest", "sha256"),
		filepath.Join(dataRoot(), "image", storageDriver, "distribution", "manifests", "sha256"),
		filepath.Join(dataRoot(), "image", storageDriver, "blobs", "sha256"),
		filepath.Join(dataRoot(), "image", storageDriver, "layerdb", "sha256"),
		filepath.Join(dataRoot(), storageDriver),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func dataRoot() string {
	if root := strings.TrimSpace(os.Getenv("TINYDOCKER_HOME")); root != "" {
		return filepath.Clean(root)
	}
	return defaultDataRoot
}

func repositoriesPath() string {
	return filepath.Join(dataRoot(), "image", storageDriver, "repositories.json")
}

func manifestPath(digest string) string {
	return filepath.Join(dataRoot(), "image", storageDriver, "distribution", "manifests", digestAlgorithm(digest), digestHexPart(digest)+".json")
}

func configPath(digest string) string {
	return filepath.Join(dataRoot(), "image", storageDriver, "imagedb", "content", digestAlgorithm(digest), digestHexPart(digest))
}

func blobPath(digest string) string {
	return filepath.Join(dataRoot(), "image", storageDriver, "blobs", digestAlgorithm(digest), digestHexPart(digest), "data")
}

func diffIDMappingPath(digest string) string {
	return filepath.Join(dataRoot(), "image", storageDriver, "distribution", "diffid-by-digest", digestAlgorithm(digest), digestHexPart(digest))
}

func layerDBDir(chainID string) string {
	return filepath.Join(dataRoot(), "image", storageDriver, "layerdb", digestAlgorithm(chainID), digestHexPart(chainID))
}

func overlayRoot() string {
	return filepath.Join(dataRoot(), storageDriver)
}

func digestAlgorithm(digest string) string {
	algorithm, _, ok := strings.Cut(digest, ":")
	if !ok || algorithm == "" {
		return "sha256"
	}
	return algorithm
}

func digestHexPart(digest string) string {
	_, hexPart, ok := strings.Cut(digest, ":")
	if !ok {
		return digest
	}
	return hexPart
}

func digestBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func composeChainID(parentChainID, diffID string) string {
	if parentChainID == "" {
		return diffID
	}
	return digestString(parentChainID + " " + diffID)
}

func digestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func randomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, data, mode); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func extractLayer(reader io.Reader, dst string) (string, error) {
	digest := sha256.New()
	tarReader, closeFn, err := newLayerTarReader(reader, digest)
	if err != nil {
		return "", err
	}
	if closeFn != nil {
		defer closeFn()
	}

	for {
		header, err := tarReader.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
			}
			return "", err
		}
		if err := applyLayerEntry(dst, header, tarReader); err != nil {
			return "", err
		}
	}
}

func newLayerTarReader(reader io.Reader, digest hash.Hash) (*tar.Reader, func() error, error) {
	buffered := bufio.NewReader(reader)
	magic, _ := buffered.Peek(4)

	if isZstdMagic(magic) {
		return nil, nil, errors.New("unsupported layer compression \"zstd\"")
	}

	var (
		stream  io.Reader = buffered
		closeFn func() error
	)
	if isGzipMagic(magic) {
		gzReader, err := gzip.NewReader(buffered)
		if err != nil {
			return nil, nil, err
		}
		stream = gzReader
		closeFn = gzReader.Close
	}

	if digest != nil {
		stream = io.TeeReader(stream, digest)
	}
	return tar.NewReader(stream), closeFn, nil
}

func applyLayerEntry(root string, header *tar.Header, tarReader *tar.Reader) error {
	cleanName := path.Clean(header.Name)
	if cleanName == "." || cleanName == "/" {
		return nil
	}

	base := path.Base(cleanName)
	dir := path.Dir(cleanName)
	if base == ".wh..wh..opq" {
		parent, err := secureJoin(root, dir)
		if err != nil {
			return err
		}
		return removeAllContents(parent)
	}
	if strings.HasPrefix(base, ".wh.") {
		parent, err := secureJoin(root, dir)
		if err != nil {
			return err
		}
		target := filepath.Join(parent, strings.TrimPrefix(base, ".wh."))
		if err := os.RemoveAll(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}

	target, err := secureJoin(root, cleanName)
	if err != nil {
		return err
	}

	switch header.Typeflag {
	case tar.TypeDir:
		if info, err := os.Lstat(target); err == nil && !info.IsDir() {
			if err := os.RemoveAll(target); err != nil {
				return err
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
			return err
		}
		return applyOwnership(target, header, false)
	case tar.TypeReg, tar.TypeRegA:
		if err := ensureParentDir(target); err != nil {
			return err
		}
		if err := os.RemoveAll(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode))
		if err != nil {
			return err
		}
		if _, err := io.Copy(file, tarReader); err != nil {
			file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		if err := applyOwnership(target, header, false); err != nil {
			return err
		}
		return os.Chtimes(target, time.Now(), header.ModTime)
	case tar.TypeSymlink:
		if err := ensureParentDir(target); err != nil {
			return err
		}
		if err := os.RemoveAll(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Symlink(header.Linkname, target); err != nil {
			return err
		}
		return applyOwnership(target, header, true)
	case tar.TypeLink:
		if err := ensureParentDir(target); err != nil {
			return err
		}
		if err := os.RemoveAll(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		linkTarget, err := secureJoin(root, resolveLayerLink(cleanName, header.Linkname))
		if err != nil {
			return err
		}
		if err := os.Link(linkTarget, target); err != nil {
			return err
		}
		return applyOwnership(target, header, false)
	case tar.TypeChar, tar.TypeBlock:
		if err := ensureParentDir(target); err != nil {
			return err
		}
		if err := os.RemoveAll(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		nodeMode := uint32(header.Mode)
		if header.Typeflag == tar.TypeChar {
			nodeMode |= syscall.S_IFCHR
		} else {
			nodeMode |= syscall.S_IFBLK
		}
		if err := syscall.Mknod(target, nodeMode, mkdev(uint32(header.Devmajor), uint32(header.Devminor))); err != nil {
			return err
		}
		return applyOwnership(target, header, false)
	case tar.TypeFifo:
		if err := ensureParentDir(target); err != nil {
			return err
		}
		if err := os.RemoveAll(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := syscall.Mkfifo(target, uint32(header.Mode)); err != nil {
			return err
		}
		return applyOwnership(target, header, false)
	default:
		return nil
	}
}

func resolveLayerLink(entryName, linkName string) string {
	_ = entryName
	if path.IsAbs(linkName) {
		return path.Clean(linkName)
	}
	return path.Clean("/" + linkName)
}

func removeAllContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func ensureParentDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

func secureJoin(root, name string) (string, error) {
	joined := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(name, "/")))
	cleanRoot := filepath.Clean(root)
	cleanJoined := filepath.Clean(joined)
	if cleanJoined != cleanRoot && !strings.HasPrefix(cleanJoined, cleanRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("layer entry escapes rootfs: %s", name)
	}
	return cleanJoined, nil
}

func applyOwnership(path string, header *tar.Header, symlink bool) error {
	if os.Geteuid() != 0 {
		return nil
	}
	if symlink {
		if err := os.Lchown(path, header.Uid, header.Gid); err != nil && !errors.Is(err, os.ErrPermission) {
			return err
		}
		return nil
	}
	if err := os.Chown(path, header.Uid, header.Gid); err != nil && !errors.Is(err, os.ErrPermission) {
		return err
	}
	return os.Chmod(path, os.FileMode(header.Mode))
}

func mkdev(major, minor uint32) int {
	return int(((major & 0xfff) << 8) | (minor & 0xff) | ((minor &^ 0xff) << 12))
}

func isGzipMagic(magic []byte) bool {
	return len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b
}

func isZstdMagic(magic []byte) bool {
	return len(magic) >= 4 && magic[0] == 0x28 && magic[1] == 0xb5 && magic[2] == 0x2f && magic[3] == 0xfd
}
